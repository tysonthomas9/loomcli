export function createPendingCommands() {
  const commands = new Map();

  return {
    add(id, timeoutMs) {
      return new Promise((resolve, reject) => {
        const timer = setTimeout(() => {
          commands.delete(id);
          reject(new Error(`CDP command ${id} timed out`));
        }, timeoutMs);
        commands.set(id, { resolve, reject, timer });
      });
    },
    settle(id, error, value) {
      const command = commands.get(id);
      if (!command) return;
      commands.delete(id);
      clearTimeout(command.timer);
      if (error) command.reject(error);
      else command.resolve(value);
    },
    rejectAll(error) {
      for (const [id, command] of commands) {
        commands.delete(id);
        clearTimeout(command.timer);
        command.reject(error);
      }
    },
    size() {
      return commands.size;
    },
  };
}

export async function chooseVisiblePage(targets, isVisible) {
  const pages = targets.filter((target) => target.type === "page" && /^https?:/.test(target.url));
  for (const target of pages) {
    if (await isVisible(target)) return target;
  }
  return pages[0] || null;
}
