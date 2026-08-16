// Package hookcfg reconciles Loom-managed native agent hooks in a workdir.
package hookcfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Event is a canonical backend hook event name.
type Event string

const (
	// UserPromptSubmit runs immediately before a submitted user turn.
	UserPromptSubmit Event = "UserPromptSubmit"

	loomCommandPrefix = "loom "
)

// HookSpec declares a command to run for a backend event.
type HookSpec struct {
	Event   Event
	Command string
}

type backendAdapter struct {
	dirName  string
	fileName string
}

var backendAdapters = map[string]backendAdapter{
	"claude": {dirName: ".claude", fileName: "settings.json"},
	// codex trust-gates repo-local hooks per handler and, in linked git
	// worktrees, reads .codex from the root checkout instead of the worktree.
	// Loom-owned containers therefore run the hook via the managed
	// /etc/codex/requirements.toml baked into the local-mode image; this
	// adapter covers host sessions, where the operator trusts it via /hooks.
	"codex": {dirName: ".codex", fileName: "hooks.json"},
}

// SupportsBackend reports whether backend has a native hook adapter.
func SupportsBackend(backend string) bool {
	_, ok := backendAdapters[backend]
	return ok
}

// Ensure reconciles Loom-managed hooks for backend in workDir. Commands owned
// by Loom are self-identifying: their command string begins with "loom ".
//
//nolint:funlen // Read, compare, reconcile, and write stay one ordered sequence.
func Ensure(workDir, backend string, specs []HookSpec) error {
	adapter, ok := backendAdapters[backend]
	if !ok {
		return fmt.Errorf("unsupported hook backend %q", backend)
	}
	if workDir == "" {
		return errors.New("hook workdir is empty")
	}
	if err := validateSpecs(specs); err != nil {
		return err
	}

	path := filepath.Join(workDir, adapter.dirName, adapter.fileName)
	original, mode, exists, err := readConfig(path)
	if err != nil {
		return err
	}

	root := orderedObject{}
	if exists {
		root, err = parseObject(original)
		if err != nil {
			return fmt.Errorf("parse existing %s: %w", path, err)
		}
	}

	hooks, err := hooksObject(root)
	if err != nil {
		return fmt.Errorf("parse hooks in %s: %w", path, err)
	}
	managed, err := managedHooks(hooks)
	if err != nil {
		return fmt.Errorf("parse hooks in %s: %w", path, err)
	}
	if exists && slices.Equal(managed, desiredHooks(specs)) {
		return nil
	}

	if err := reconcileHooks(&hooks, specs); err != nil {
		return fmt.Errorf("reconcile hooks in %s: %w", path, err)
	}
	hooksJSON, err := hooks.marshalJSON()
	if err != nil {
		return fmt.Errorf("encode hooks for %s: %w", path, err)
	}
	root.set("hooks", hooksJSON)
	updated, err := root.marshalIndent()
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if bytes.Equal(original, updated) {
		return nil
	}
	if err := writeConfig(path, updated, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func validateSpecs(specs []HookSpec) error {
	seen := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		if spec.Event == "" {
			return errors.New("hook event is empty")
		}
		if !strings.HasPrefix(spec.Command, loomCommandPrefix) {
			return fmt.Errorf("managed hook command %q must begin with %q", spec.Command, loomCommandPrefix)
		}
		key := string(spec.Event) + "\x00" + spec.Command
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate hook spec for %s: %q", spec.Event, spec.Command)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func readConfig(path string) ([]byte, fs.FileMode, bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is selected inside the caller-provided workdir.
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0o600, false, nil
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("read %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, false, fmt.Errorf("stat %s: %w", path, err)
	}
	return data, info.Mode().Perm(), true, nil
}

func writeConfig(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".hooks-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func hooksObject(root orderedObject) (orderedObject, error) {
	raw, ok := root.get("hooks")
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return orderedObject{}, nil
	}
	return parseObject(raw)
}

type managedHook struct {
	event   string
	command string
}

func desiredHooks(specs []HookSpec) []managedHook {
	desired := make([]managedHook, 0, len(specs))
	for _, spec := range specs {
		desired = append(desired, managedHook{event: string(spec.Event), command: spec.Command})
	}
	slices.SortFunc(desired, compareManagedHooks)
	return desired
}

func managedHooks(hooks orderedObject) ([]managedHook, error) {
	var managed []managedHook
	for _, event := range hooks.fields {
		groups, err := parseArray(event.value)
		if err != nil {
			return nil, fmt.Errorf("event %s: %w", event.key, err)
		}
		for _, groupRaw := range groups {
			group, err := parseObject(groupRaw)
			if err != nil {
				return nil, fmt.Errorf("event %s matcher group: %w", event.key, err)
			}
			hooksRaw, ok := group.get("hooks")
			if !ok {
				continue
			}
			commands, err := parseArray(hooksRaw)
			if err != nil {
				return nil, fmt.Errorf("event %s command hooks: %w", event.key, err)
			}
			for _, commandRaw := range commands {
				command, err := hookCommand(commandRaw)
				if err != nil {
					return nil, fmt.Errorf("event %s command hook: %w", event.key, err)
				}
				if strings.HasPrefix(command, loomCommandPrefix) {
					managed = append(managed, managedHook{event: event.key, command: command})
				}
			}
		}
	}
	slices.SortFunc(managed, compareManagedHooks)
	return managed, nil
}

func compareManagedHooks(a, b managedHook) int {
	if byEvent := strings.Compare(a.event, b.event); byEvent != 0 {
		return byEvent
	}
	return strings.Compare(a.command, b.command)
}

//nolint:gocognit,funlen // Removing loom entries while preserving user groups walks one nested schema.
func reconcileHooks(hooks *orderedObject, specs []HookSpec) error {
	for eventIndex := range hooks.fields {
		event := &hooks.fields[eventIndex]
		groups, err := parseArray(event.value)
		if err != nil {
			return fmt.Errorf("event %s: %w", event.key, err)
		}
		keptGroups := make([]json.RawMessage, 0, len(groups))
		for _, groupRaw := range groups {
			group, err := parseObject(groupRaw)
			if err != nil {
				return fmt.Errorf("event %s matcher group: %w", event.key, err)
			}
			hooksRaw, ok := group.get("hooks")
			if !ok {
				keptGroups = append(keptGroups, groupRaw)
				continue
			}
			commands, err := parseArray(hooksRaw)
			if err != nil {
				return fmt.Errorf("event %s command hooks: %w", event.key, err)
			}
			keptCommands := make([]json.RawMessage, 0, len(commands))
			removed := false
			for _, commandRaw := range commands {
				command, err := hookCommand(commandRaw)
				if err != nil {
					return fmt.Errorf("event %s command hook: %w", event.key, err)
				}
				if strings.HasPrefix(command, loomCommandPrefix) {
					removed = true
					continue
				}
				keptCommands = append(keptCommands, commandRaw)
			}
			if removed && len(keptCommands) == 0 {
				continue
			}
			if removed {
				group.set("hooks", marshalArray(keptCommands))
				groupRaw, err = group.marshalJSON()
				if err != nil {
					return err
				}
			}
			keptGroups = append(keptGroups, groupRaw)
		}
		event.value = marshalArray(keptGroups)
	}

	byEvent := make(map[string][]string)
	var eventOrder []string
	for _, spec := range specs {
		event := string(spec.Event)
		if _, ok := byEvent[event]; !ok {
			eventOrder = append(eventOrder, event)
		}
		byEvent[event] = append(byEvent[event], spec.Command)
	}
	for _, event := range eventOrder {
		groupsRaw, _ := hooks.get(event)
		groups := []json.RawMessage{}
		if len(groupsRaw) != 0 {
			var err error
			groups, err = parseArray(groupsRaw)
			if err != nil {
				return fmt.Errorf("event %s: %w", event, err)
			}
		}
		groups = append(groups, managedGroup(byEvent[event]))
		hooks.set(event, marshalArray(groups))
	}
	return nil
}

func hookCommand(raw json.RawMessage) (string, error) {
	hook, err := parseObject(raw)
	if err != nil {
		return "", err
	}
	commandRaw, ok := hook.get("command")
	if !ok {
		return "", nil
	}
	var command string
	if err := json.Unmarshal(commandRaw, &command); err != nil {
		return "", fmt.Errorf("command: %w", err)
	}
	return command, nil
}

func managedGroup(commands []string) json.RawMessage {
	hooks := make([]json.RawMessage, 0, len(commands))
	for _, command := range commands {
		commandJSON, _ := json.Marshal(command)
		hooks = append(hooks, json.RawMessage(`{"type":"command","command":`+string(commandJSON)+`}`))
	}
	return json.RawMessage(`{"matcher":"","hooks":` + string(marshalArray(hooks)) + `}`)
}

func parseArray(raw json.RawMessage) ([]json.RawMessage, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	if values == nil {
		values = []json.RawMessage{}
	}
	return values, nil
}

func marshalArray(values []json.RawMessage) json.RawMessage {
	var out bytes.Buffer
	out.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			out.WriteByte(',')
		}
		out.Write(value)
	}
	out.WriteByte(']')
	return out.Bytes()
}

type objectField struct {
	key   string
	value json.RawMessage
}

type orderedObject struct {
	fields []objectField
}

func parseObject(raw []byte) (orderedObject, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return orderedObject{}, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return orderedObject{}, errors.New("expected JSON object")
	}
	result := orderedObject{}
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return orderedObject{}, err
		}
		key, ok := token.(string)
		if !ok {
			return orderedObject{}, errors.New("expected object key")
		}
		if _, duplicate := seen[key]; duplicate {
			return orderedObject{}, fmt.Errorf("duplicate object key %q", key)
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return orderedObject{}, err
		}
		result.fields = append(result.fields, objectField{key: key, value: value})
	}
	if _, err := decoder.Token(); err != nil {
		return orderedObject{}, err
	}
	if decoder.More() {
		return orderedObject{}, errors.New("unexpected JSON after object")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return orderedObject{}, err
		}
		return orderedObject{}, errors.New("unexpected JSON after object")
	}
	return result, nil
}

func (o orderedObject) get(key string) (json.RawMessage, bool) {
	for _, field := range o.fields {
		if field.key == key {
			return field.value, true
		}
	}
	return nil, false
}

func (o *orderedObject) set(key string, value json.RawMessage) {
	for i := range o.fields {
		if o.fields[i].key == key {
			o.fields[i].value = value
			return
		}
	}
	o.fields = append(o.fields, objectField{key: key, value: value})
}

func (o orderedObject) marshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, field := range o.fields {
		if i > 0 {
			out.WriteByte(',')
		}
		key, _ := json.Marshal(field.key)
		out.Write(key)
		out.WriteByte(':')
		if !json.Valid(field.value) {
			return nil, fmt.Errorf("field %q contains invalid JSON", field.key)
		}
		out.Write(field.value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

func (o orderedObject) marshalIndent() ([]byte, error) {
	compact, err := o.marshalJSON()
	if err != nil {
		return nil, err
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, compact, "", "  "); err != nil {
		return nil, err
	}
	indented.WriteByte('\n')
	return indented.Bytes(), nil
}
