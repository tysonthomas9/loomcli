export function createQueuedActionState(initialAppId = "app-a") {
  let activeAppId = initialAppId;
  let queuedAction = null;
  const epochs = new Map();

  return {
    select(appId) {
      activeAppId = appId;
    },
    setEpoch(appId, epoch) {
      epochs.set(appId, epoch);
    },
    queue(appId = activeAppId, epoch = epochs.get(appId) ?? 0) {
      queuedAction = { appId, epoch };
      return { ...queuedAction };
    },
    current() {
      return queuedAction ? { ...queuedAction } : null;
    },
  };
}

export function agentBrowserCommand(appId) {
  if (!/^app-[a-z]$/.test(appId)) throw new Error("invalid browser app id");
  return `desktop/prototypes/kernel-browser/scripts/agent-browser-control.sh ${appId} snapshot -i`;
}

export function mergeBrowserApps(currentApps, discoveredApps) {
  const merged = new Map(currentApps.map((app) => [app.id, app]));
  for (const app of discoveredApps) merged.set(app.id, app);
  return [...merged.values()].sort((left, right) => left.id.localeCompare(right.id));
}
