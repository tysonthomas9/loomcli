// Package appstores consolidates Redis-backed store initialization for the
// webui/app composition root. The app package imports this single package
// instead of issuetabs, sessionhistory, tabmeta, and subscription individually.
package appstores

import (
	"context"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/subscription"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// Type aliases so the app package can reference concrete types without
// importing the underlying packages directly.

// TabMetaStore is a type alias for tabmeta.Store.
type TabMetaStore = tabmeta.Store

// IssueTabStore is a type alias for issuetabs.Store.
type IssueTabStore = issuetabs.Store

// SessionHistoryStore is a type alias for sessionhistory.Store.
type SessionHistoryStore = sessionhistory.Store

// MultiWorkspaceSubscriber is a type alias for subscription.MultiWorkspaceSubscriber.
type MultiWorkspaceSubscriber = subscription.MultiWorkspaceSubscriber

// ActivationReason records why a workspace subscriber was activated.
type ActivationReason = subscription.ActivationReason

const (
	ActivationReasonHTTP     = subscription.ActivationReasonHTTP
	ActivationReasonRegistry = subscription.ActivationReasonRegistry
	ActivationReasonSSE      = subscription.ActivationReasonSSE
)

// SessionRecord is a type alias for sessionhistory.SessionRecord.
type SessionRecord = sessionhistory.SessionRecord

// MutationsSinceFn is the type for the getMutationsSince callback.
type MutationsSinceFn = func(wsID string, since string) []realtime.MutationEvent

// Hub is a type alias for realtime.Hub.
type Hub = realtime.Hub

// TerminalAuth is a type alias for realtime.TerminalAuth.
type TerminalAuth = realtime.TerminalAuth

// TokenStore is a type alias for realtime.TokenStore.
type TokenStore = realtime.TokenStore

// NewHub creates a new SSE hub.
func NewHub() *Hub { return realtime.NewHub() }

// NewTerminalAuth creates a new terminal auth instance.
func NewTerminalAuth() (*TerminalAuth, error) { return realtime.NewTerminalAuth() }

// NewTokenStore creates a new SSE token store.
func NewTokenStore() (*TokenStore, error) { return realtime.NewTokenStore() }

// NewMultiSub creates and starts a multi-workspace subscriber bridging backend
// mutations to SSE.
func NewMultiSub(ctx context.Context, hub *realtime.Hub, logger *slog.Logger) *MultiWorkspaceSubscriber {
	return subscription.NewStartedMultiWorkspaceSubscriber(ctx, hub, logger)
}

// GetMutationsSinceFn returns the mutations-since callback from the subscriber.
func GetMutationsSinceFn(sub *MultiWorkspaceSubscriber) func(wsID string, since string) []realtime.MutationEvent {
	if sub == nil {
		return nil
	}
	return sub.GetMutationsSinceForWorkspace
}

// InitTabMeta creates the tab metadata store from Redis config.
func InitTabMeta(_ context.Context, redisCfg *fleet.RedisConfig, logger *slog.Logger) (*TabMetaStore, func()) {
	tmClient := fleet.NewRedisClient(redisCfg.Address, redisCfg.Password, 0)
	store := tabmeta.NewStore(tmClient, nil)
	cleanup := func() { _ = store.Close() }
	logger.Info("tab metadata store initialized", "redis_address", redisCfg.Address)
	return store, cleanup
}

// InitIssueTabs creates the issue tab store from Redis config.
func InitIssueTabs(ctx context.Context, redisCfg *fleet.RedisConfig, initialWSID string, logger *slog.Logger) (*IssueTabStore, func()) {
	itClient := fleet.NewRedisClient(redisCfg.Address, redisCfg.Password, 0)
	store := issuetabs.NewStore(itClient, nil)
	cleanup := func() { _ = store.Close() }
	logger.Info("issue tab store initialized", "redis_address", redisCfg.Address)
	return store, cleanup
}

// InitSessionHistory creates the session history store from Redis config.
func InitSessionHistory(ctx context.Context, redisCfg *fleet.RedisConfig, initialWSID string, logger *slog.Logger) (*SessionHistoryStore, func()) {
	shClient := fleet.NewRedisClient(redisCfg.Address, redisCfg.Password, 0)
	store := sessionhistory.NewStore(shClient, nil)
	cleanup := func() { _ = store.Close() }
	logger.Info("session history store initialized", "redis_address", redisCfg.Address)
	return store, cleanup
}

// ValidateIssueID validates an issue ID string.
func ValidateIssueID(issueID string) error {
	return sessionhistory.ValidateIssueID(issueID)
}

// SubscriptionModule is a type alias for subscription.Module.
type SubscriptionModule = subscription.Module

// NewSubscriptionModule creates a new SSE subscription module.
func NewSubscriptionModule(
	hub *realtime.Hub,
	getMutationsSince func(string, string) []realtime.MutationEvent,
	wsFromCtx func(context.Context) string,
	activateWorkspace func(context.Context, string),
	sseTokens *realtime.TokenStore,
) *SubscriptionModule {
	return subscription.NewModule(hub, getMutationsSince, wsFromCtx, activateWorkspace, sseTokens)
}
