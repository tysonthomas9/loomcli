package publish

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/changeset"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/replay"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type Result struct {
	Revision      loomgit.Revision
	PRURL         string
	PRNumber      int
	AlreadyExists bool
	Backend       string
	StatusReason  string
}

var localPublishProvider = func() (Forge, string, string) { return nil, "", "" }
var localFlagForTask = taskFeatureFlag

func LeadStackID(lead string) string {
	return fmt.Sprintf("lead-%x", sha256.Sum256([]byte(lead)))
}

func DeliveryModeLocal(ctx context.Context, workspace string) (string, error) {
	store, err := openLocalStore()
	if err != nil {
		return "", err
	}
	defer func() { _ = store.Close() }()
	return store.DeliveryMode(ctx, workspace)
}

func SetDeliveryModeLocal(ctx context.Context, workspace, mode string) error {
	store, err := openLocalStore()
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	return store.SetDeliveryMode(ctx, workspace, mode)
}

// PublishLocal resolves a recorded change and its lead working area before publishing.
func PublishLocal(ctx context.Context, workspace, lead, change string) (Result, error) {
	if workspace == "" || lead == "" || change == "" {
		return Result{}, errors.New("workspace, lead and change are required")
	}
	store, err := openLocalStore()
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = store.Close() }()
	cfg, err := config.LoadConfig()
	if err != nil {
		return Result{}, err
	}
	forge, token, slug := localPublishProvider()
	return publishRecorded(ctx, store, cfg, workspace, lead, change, forge, token, slug, localFlagForTask)
}

// PublishStackLocal publishes the requested applied layers in working-area order.
func PublishStackLocal(ctx context.Context, workspace, stackID, lead string, changes []string) ([]Result, error) {
	forge, token, slug := localPublishProvider()
	return PublishStackWithProvider(ctx, workspace, stackID, lead, changes, forge, token, slug)
}

func PublishStackWithProvider(ctx context.Context, workspace, stackID, lead string, changes []string, forge Forge, token, slug string) ([]Result, error) {
	if workspace == "" || stackID == "" || lead == "" {
		return nil, errors.New("workspace, stack ID and lead are required")
	}
	store, err := openLocalStore()
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, err
	}
	return publishStackRecorded(ctx, store, cfg, workspace, stackID, lead, changes, forge, token, slug)
}

func PublishLeadChangeLocal(ctx context.Context, workspace, lead, change string) (Result, error) {
	store, err := openLocalStore()
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = store.Close() }()
	repoName, err := repoNameForStack(ctx, store, workspace, lead, nil)
	if err != nil {
		return Result{}, err
	}
	area, err := workingArea(ctx, store, workspace, lead, repoName)
	if err != nil {
		return Result{}, err
	}
	changes, err := orderedStackChanges(ctx, store, area.Path, workspace, lead, nil)
	if err != nil {
		return Result{}, err
	}
	if !slices.Contains(changes, change) {
		return Result{}, fmt.Errorf("change %q is not in the lead's applied stack", change)
	}
	requested, err := taskStackChanges(ctx, store, workspace, lead)
	if err != nil {
		return Result{}, err
	}
	stackID, err := recordedStackID(ctx, store, workspace, lead, changes)
	if err != nil {
		return Result{}, err
	}
	results, err := PublishStackLocal(ctx, workspace, stackID, lead, requested)
	if err != nil {
		return Result{}, err
	}
	for _, result := range results {
		if result.Revision.Change == change {
			return result, nil
		}
	}
	return Result{}, errors.New("published stack omitted the requested change")
}

func recordedStackID(ctx context.Context, store *journal.SQLite, workspace, lead string, changes []string) (string, error) {
	for _, change := range changes {
		publication, found, err := store.Publication(ctx, workspace, change)
		if err != nil {
			return "", err
		}
		if found && publication.StackID != "" {
			return publication.StackID, nil
		}
	}
	return LeadStackID(lead), nil
}

func taskStackChanges(ctx context.Context, store *journal.SQLite, workspace, lead string) ([]string, error) {
	applied, err := store.AppliedLog(ctx, workspace, lead)
	if err != nil {
		return nil, err
	}
	return requestedFromApplied(applied), nil
}

func requestedFromApplied(applied []loomgit.AppliedLayer) []string {
	requested := make([]string, 0, len(applied))
	for _, layer := range applied {
		if layer.Revision > 0 && strings.HasPrefix(layer.Change, "own-") {
			continue
		}
		requested = append(requested, layer.Change)
	}
	return requested
}

var featureFlagName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func taskFeatureFlag(ctx context.Context, workspace, task string) (string, error) {
	ctx = middleware.WithWorkspace(ctx, workspace)
	detail, err := cli.DefaultIssueBackend().Get(ctx, task)
	if err != nil {
		return "", fmt.Errorf("load task %s feature flag: %w", task, err)
	}
	if detail == nil {
		return "", fmt.Errorf("task %s is unavailable", task)
	}
	return featureFlagFromLabels(detail.Labels)
}

func featureFlagFromLabels(labels []string) (string, error) {
	var selected string
	for _, label := range labels {
		name, ok := strings.CutPrefix(label, "feature-flag:")
		if !ok {
			continue
		}
		if !featureFlagName.MatchString(name) || selected != "" {
			return "", errors.New("task has an invalid or repeated feature flag label")
		}
		selected = name
	}
	return selected, nil
}

func openLocalStore() (*journal.SQLite, error) {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); err != nil {
		return nil, loomgit.NewError(loomgit.WorkspaceUnsupported, "revision journal is unavailable", err)
	}
	store, err := journal.OpenSQLite(path)
	return store, err
}

func publishStackRecorded(ctx context.Context, store *journal.SQLite, cfg *config.LoomConfig, workspace, stackID, lead string, changes []string, forge Forge, token, slug string) ([]Result, error) {
	if err := requireStackMode(ctx, store, workspace); err != nil {
		return nil, err
	}
	repoName, err := repoNameForStack(ctx, store, workspace, lead, changes)
	if err != nil {
		return nil, err
	}
	area, err := workingArea(ctx, store, workspace, lead, repoName)
	if err != nil {
		return nil, err
	}
	changes, err = orderedStackChanges(ctx, store, area.Path, workspace, lead, changes)
	if err != nil {
		return nil, err
	}
	prior, err := existingStackPRs(ctx, store, workspace, changes)
	if err != nil {
		return nil, err
	}
	_, configured, found := config.WorkspaceByID(cfg, workspace)
	if !found {
		return nil, loomgit.NewError(loomgit.WorkspaceUnsupported, "workspace is unavailable", nil)
	}
	for _, repo := range configured.Repos {
		if repo.Name != repoName {
			continue
		}
		forge, token, slug, err = configuredStackProvider(ctx, repo.ResolveAbsPath(configured.Path), forge, token, slug)
		if err != nil {
			return nil, err
		}
		backend, err := chooseStackBackend(ctx, store, workspace, stackID, slug, forge, LoomStackBackend{Store: store}, GitHubStackBackend{Store: store})
		if err != nil {
			return nil, err
		}
		revisions, err := backend.Publish(ctx, StackRequest{Request: Request{
			Workspace: workspace, Lead: lead, Repo: repo.ResolveAbsPath(configured.Path),
			WorkingArea: area.Path, BaseSHA: area.BaseSHA, RepoName: repoName, forge: forge, token: token, slug: slug,
		}, StackID: stackID, Changes: changes})
		if err != nil {
			return nil, err
		}
		return stackResults(ctx, store, workspace, changes, revisions, backend.Capabilities(), prior)
	}
	return nil, loomgit.NewError(loomgit.RepoSelectionRequired, "stack repo is not in the workspace", nil)
}

func configuredStackProvider(ctx context.Context, repoPath string, forge Forge, token, slug string) (Forge, string, string, error) {
	if forge == nil {
		if token == "" {
			token = githubtoken.GitHub(ctx)
		}
		if token == "" {
			return nil, "", "", errors.New("GitHub host credential unavailable")
		}
		forge = stackpublish.NewConfiguredGitHubForge(token)
	}
	if slug == "" {
		var err error
		slug, err = stackSlug(ctx, repoPath)
		if err != nil {
			return nil, "", "", err
		}
	}
	return forge, token, slug, nil
}

func existingStackPRs(ctx context.Context, store *journal.SQLite, workspace string, changes []string) (map[string]bool, error) {
	prior := make(map[string]bool, len(changes))
	for _, change := range changes {
		publication, found, err := store.Publication(ctx, workspace, change)
		if err != nil {
			return nil, err
		}
		prior[change] = found && publication.PRNumber > 0
	}
	return prior, nil
}

func stackResults(ctx context.Context, store *journal.SQLite, workspace string, changes []string, revisions []loomgit.Revision, capabilities StackCapabilities, prior map[string]bool) ([]Result, error) {
	results := make([]Result, 0, len(revisions))
	backend, reason := "native", ""
	if !capabilities.NativeStacks {
		backend, reason = "loom", "GitHub native stacks are unavailable for this repository"
	}
	for index, revision := range revisions {
		publication, found, err := store.Publication(ctx, workspace, changes[index])
		if err != nil || !found {
			return nil, errors.New("stack publication record unavailable")
		}
		results = append(results, Result{Revision: revision, PRURL: publication.PRURL, PRNumber: publication.PRNumber, AlreadyExists: prior[changes[index]], Backend: backend, StatusReason: reason})
	}
	return results, nil
}

func orderedStackChanges(ctx context.Context, store *journal.SQLite, areaPath, workspace, lead string, changes []string) ([]string, error) {
	areaRunner, err := gitexec.New(areaPath, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return nil, err
	}
	applied, err := apply.New(store, nil, areaRunner).AppliedLog(ctx, workspace, lead)
	if err != nil {
		return nil, err
	}
	ordered := make([]string, 0, len(applied))
	for _, layer := range applied {
		ordered = append(ordered, layer.Change)
	}
	if changes == nil {
		return ordered, nil
	}
	if slices.Equal(changes, ordered) {
		return ordered, nil
	}
	requested := requestedFromApplied(applied)
	if len(requested) != len(changes) {
		return nil, loomgit.NewError(loomgit.StackNotLinear, "requested tasks differ from working-area layers", nil)
	}
	for index, change := range changes {
		if requested[index] != change {
			return nil, loomgit.NewError(loomgit.StackNotLinear, "requested tasks differ from working-area layer order", nil)
		}
	}
	return ordered, nil
}

func stackSlug(ctx context.Context, repoPath string) (string, error) {
	runner, err := gitexec.New(repoPath, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return "", err
	}
	remote, err := runner.Run(ctx, "remote", "get-url", "--push", "origin")
	if err != nil {
		return "", err
	}
	return GitHubSlug(strings.TrimSpace(string(remote)))
}

func repoNameForStack(ctx context.Context, store *journal.SQLite, workspace, lead string, changes []string) (string, error) {
	if len(changes) == 0 {
		areas, err := store.WorkingAreas(ctx, workspace, lead)
		if err != nil {
			return "", err
		}
		if len(areas) != 1 || areas[0].Repo == "" {
			return "", loomgit.NewError(loomgit.RepoSelectionRequired, "own-only stack requires one recorded working area", nil)
		}
		return areas[0].Repo, nil
	}
	repoName, err := store.RepoForChange(ctx, workspace, changes[0])
	if err != nil {
		return "", err
	}
	for _, change := range changes[1:] {
		other, err := store.RepoForChange(ctx, workspace, change)
		if err != nil {
			return "", err
		}
		if other != repoName {
			return "", loomgit.NewError(loomgit.StackNotLinear, "stack changes span repositories", nil)
		}
	}
	return repoName, nil
}

func publishRecorded(ctx context.Context, store *journal.SQLite, cfg *config.LoomConfig, workspace, lead, change string,
	forge Forge, token, slug string, flagForTask func(context.Context, string, string) (string, error)) (Result, error) {
	repoName, err := store.RepoForChange(ctx, workspace, change)
	if err != nil {
		return Result{}, fmt.Errorf("find repo for change: %w", err)
	}
	area, err := workingArea(ctx, store, workspace, lead, repoName)
	if err != nil {
		return Result{}, err
	}
	_, configured, found := config.WorkspaceByID(cfg, workspace)
	if !found {
		return Result{}, loomgit.NewError(loomgit.WorkspaceUnsupported, "workspace is unavailable", nil)
	}
	for _, repo := range configured.Repos {
		if repo.Name != repoName {
			continue
		}
		request := Request{Workspace: workspace, Lead: lead, Change: change,
			Repo: repo.ResolveAbsPath(configured.Path), WorkingArea: area.Path,
			BaseSHA: area.BaseSHA, RepoName: repoName, forge: forge, token: token, slug: slug}
		return publishRepo(ctx, store, request, flagForTask)
	}
	return Result{}, loomgit.NewError(loomgit.RepoSelectionRequired, "change repo is not in the workspace", nil)
}

func publishRepo(ctx context.Context, store *journal.SQLite, request Request,
	flagForTask func(context.Context, string, string) (string, error)) (Result, error) {
	prior, priorExists, err := store.Publication(ctx, request.Workspace, request.Change)
	if err != nil {
		return Result{}, err
	}
	mode, err := store.DeliveryMode(ctx, request.Workspace)
	if err != nil {
		return Result{}, err
	}
	if mode == "trunk" {
		if err := prepareTrunkRequest(ctx, store, &request, flagForTask); err != nil {
			return Result{}, err
		}
	}
	revision, err := Publish(ctx, store, request)
	if err != nil {
		if mode == "trunk" && errors.Is(err, loomgit.NewError(loomgit.ReviewRequired, "", nil)) {
			if holdErr := store.SetDeliveryHold(ctx, request.Workspace, request.Change, "waiting_on_verdict"); holdErr != nil {
				return Result{}, holdErr
			}
		}
		return Result{}, err
	}
	if mode == "trunk" {
		if err := store.SetDeliveryHold(ctx, request.Workspace, request.Change, ""); err != nil {
			return Result{}, err
		}
	}
	publication, found, err := store.Publication(ctx, request.Workspace, request.Change)
	if err != nil {
		return Result{}, err
	}
	if !found || publication.Head != revision.HeadSHA || publication.Phase != "done" {
		return Result{}, errors.New("published PR record unavailable")
	}
	return Result{Revision: revision, PRURL: publication.PRURL, PRNumber: publication.PRNumber,
		AlreadyExists: priorExists && prior.Phase == "done" && prior.Head == revision.HeadSHA}, nil
}

func prepareTrunkRequest(ctx context.Context, store *journal.SQLite, request *Request,
	flagForTask func(context.Context, string, string) (string, error)) error {
	predecessor, err := store.DependencyForChange(ctx, request.Workspace, request.Change)
	if err != nil {
		return err
	}
	if predecessor != "" {
		landed, err := store.IsLanded(ctx, request.Workspace, predecessor)
		if err != nil {
			return err
		}
		if !landed {
			if err := store.SetDeliveryHold(ctx, request.Workspace, request.Change, "waiting_on_dependency"); err != nil {
				return err
			}
			return loomgit.NewError(loomgit.LineageUnresolved, "waiting_on_dependency: predecessor has not landed", nil)
		}
	}
	if flagForTask != nil {
		task, err := store.TaskForChange(ctx, request.Workspace, request.Change)
		if err != nil {
			return err
		}
		if task != "" {
			request.FeatureFlag, err = flagForTask(ctx, request.Workspace, task)
			if err != nil {
				return err
			}
		}
	}
	request.RevisionHead, err = trunkRevision(ctx, store, *request)
	return err
}

func trunkRevision(ctx context.Context, store *journal.SQLite, req Request) (string, error) {
	runner, err := gitexec.New(req.Repo, gitexec.Options{})
	if err != nil {
		return "", err
	}
	area, err := gitexec.New(req.WorkingArea, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return "", err
	}
	initialHead, err := layerHead(ctx, apply.New(store, nil, area), area, req.Workspace, req.Lead, req.BaseSHA, req.Change)
	if err != nil {
		return "", err
	}
	source, err := store.RevisionByHead(ctx, req.Workspace, req.Change, initialHead)
	if err != nil {
		return "", err
	}
	trunk, err := recordedTrunk(ctx, store, req)
	if err != nil {
		return "", err
	}
	if _, err := runner.Run(ctx, "fetch", "origin", trunk); err != nil {
		return "", fmt.Errorf("fetch trunk: %w", err)
	}
	out, err := runner.Run(ctx, "rev-parse", "--verify", "refs/remotes/origin/"+trunk+"^{commit}")
	if err != nil {
		return "", err
	}
	base := strings.TrimSpace(string(out))
	if source.BaseSHA == base {
		return source.HeadSHA, nil
	}
	return deriveTrunkRevision(ctx, store, runner, req, source, base)
}

func deriveTrunkRevision(ctx context.Context, store *journal.SQLite, runner *gitexec.Runner,
	req Request, source loomgit.Revision, base string) (string, error) {
	result, err := replay.New(runner).TrialMerge(ctx, source.BaseSHA, source.HeadSHA, base)
	if err != nil {
		return "", err
	}
	if result.ConflictCommit != "" {
		return "", loomgit.NewError(loomgit.Conflict, strings.Join(result.ConflictingPaths, ", "), nil)
	}
	derived, err := changeset.RecordDerived(ctx, store, runner, changeset.DerivedInput{
		Workspace: req.Workspace, Change: req.Change,
		RequestID:  "trunk-delivery:" + req.Workspace + ":" + req.Change + ":" + strconv.Itoa(source.Number) + ":" + base,
		FromNumber: source.Number, Operation: "restack", BaseSHA: base,
		HeadSHA: result.HeadSHA, Outcome: source.Outcome,
	})
	if err != nil {
		return "", err
	}
	if err := review.RequireVerdict(ctx, store, req.Workspace, req.Change, derived.Number, derived.HeadSHA, "publish", ""); err != nil {
		if !errors.Is(err, loomgit.NewError(loomgit.ReviewRequired, "", nil)) {
			return "", err
		}
		if _, _, err := review.CarryForward(ctx, store, runner, source, derived, result); err != nil {
			return "", err
		}
	}
	return derived.HeadSHA, nil
}

func workingArea(ctx context.Context, store *journal.SQLite, workspace, lead, repoName string) (*journal.WorkingArea, error) {
	areas, err := store.WorkingAreas(ctx, workspace, lead)
	if err != nil {
		return nil, err
	}
	var area *journal.WorkingArea
	for index := range areas {
		if areas[index].Repo != repoName {
			continue
		}
		if area != nil {
			return nil, loomgit.NewError(loomgit.AttentionRequired, "ambiguous working area for change repo and lead", nil)
		}
		area = &areas[index]
	}
	if area == nil || area.Path == "" {
		return nil, loomgit.NewError(loomgit.AttentionRequired, "working area for change repo and lead is unavailable", nil)
	}
	return area, nil
}
