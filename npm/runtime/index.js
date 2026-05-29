export function defineConfig(config) {
  return config || {};
}

export function defineAgent(agent) {
  return agent || {};
}

export const createAgent = defineAgent;

export function defineAgentProfile(profile) {
  return profile || {};
}

export function defineSkill(skill) {
  return skill || {};
}

export function defineWorkflow(workflow) {
  return workflow || {};
}

export function defineTool(tool) {
  return tool || {};
}

export const runtime = {
  local(config = {}) {
    return { provider: "local", ...config };
  },
  podman(config = {}) {
    return { provider: "other", ...config };
  },
  remote(config = {}) {
    return { provider: config.provider || "e2b", ...config };
  },
};

export const trigger = {
  issueLabelAdded(config = {}) {
    return { event: "issue.label_added", filter: config };
  },
};

export const schema = new Proxy(
  {},
  {
    get(_target, prop) {
      return (...args) => ({ kind: String(prop), args });
    },
  },
);
