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
