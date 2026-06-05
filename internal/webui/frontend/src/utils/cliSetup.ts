import type { BackendInfo } from "@/utils/workspace";

export type AIBackendSetupAction =
  | "install"
  | "login"
  | "configure"
  | "set-default"
  | "test";

export interface CliSetupRequest {
  id: string;
  backendName: string;
  displayName: string;
  provider: string;
  brandColor: string;
  action: AIBackendSetupAction;
}

export interface CliSetupInstructions {
  title: string;
  description: string;
  command?: string;
  buttonLabel: string;
}

export const CLI_SETUP_REQUEST_KEY = "loom-cli-setup-request";
export const CLI_SETUP_REQUEST_EVENT = "loom-cli-setup-requested";

const INSTALL_COMMANDS: Record<string, string> = {
  claude: "npm install -g @anthropic-ai/claude-code",
  codex: "npm install -g @openai/codex",
  cursor: "curl https://cursor.com/install -fsS | bash",
  gemini: "npm install -g @google/gemini-cli",
  opencode: "npm install -g opencode-ai",
};

const LOGIN_COMMANDS: Record<string, string> = {
  claude: "claude login",
  codex: "codex login",
  gemini: "gemini",
  opencode: "opencode auth login",
};

function makeRequestId(): string {
  return `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function commandFor(
  backendName: string,
  action: AIBackendSetupAction,
): string | undefined {
  switch (action) {
    case "install":
      return INSTALL_COMMANDS[backendName];
    case "login":
    case "configure":
      return LOGIN_COMMANDS[backendName];
    case "test":
      // Flue has no global CLI; its runtime is Node, so verify that instead.
      return backendName === "flue" ? "node --version" : `${backendName} --version`;
    case "set-default":
      return undefined;
  }
}

function actionTitle(action: AIBackendSetupAction): string {
  switch (action) {
    case "install":
      return "Install";
    case "login":
      return "Log in";
    case "configure":
      return "Configure";
    case "test":
      return "Test";
    case "set-default":
      return "Set default";
  }
}

export function getCliSetupInstructions(
  request: CliSetupRequest,
): CliSetupInstructions {
  const command = commandFor(request.backendName, request.action);
  const action = actionTitle(request.action);

  if (!command) {
    const description =
      request.backendName === "flue"
        ? "Flue is a managed Node runtime — there's no global CLI to install or log in to. loom bootstraps ~/.loom/flue automatically and uses your model-provider key (ANTHROPIC_API_KEY / OPENAI_API_KEY / …) or your local codex login. Just ensure Node >= 22.18 is on PATH."
        : "No automated terminal command is available for this CLI. Use the vendor installer, then make sure the CLI is on PATH for this workspace.";
    return {
      title: `${action} ${request.displayName}`,
      description,
      buttonLabel: "Focus setup shell",
    };
  }

  return {
    title: `${action} ${request.displayName}`,
    description:
      request.action === "install"
        ? "The backend will start this install command in a setup terminal. Leave it open until it finishes."
        : "The backend will start this interactive command in a setup terminal. Follow the prompts there.",
    command,
    buttonLabel: request.action === "install" ? "Run install" : "Run command",
  };
}

function parseRequest(raw: string | null): CliSetupRequest | null {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Partial<CliSetupRequest>;
    if (
      typeof parsed.id !== "string" ||
      typeof parsed.backendName !== "string" ||
      typeof parsed.displayName !== "string" ||
      typeof parsed.provider !== "string" ||
      typeof parsed.brandColor !== "string" ||
      typeof parsed.action !== "string"
    ) {
      return null;
    }
    return {
      id: parsed.id,
      backendName: parsed.backendName,
      displayName: parsed.displayName,
      provider: parsed.provider,
      brandColor: parsed.brandColor,
      action: parsed.action as AIBackendSetupAction,
    };
  } catch {
    return null;
  }
}

export function readPendingCliSetupRequest(): CliSetupRequest | null {
  try {
    return parseRequest(sessionStorage.getItem(CLI_SETUP_REQUEST_KEY));
  } catch {
    return null;
  }
}

export function clearPendingCliSetupRequest(id?: string): void {
  try {
    if (id) {
      const current = readPendingCliSetupRequest();
      if (current && current.id !== id) return;
    }
    sessionStorage.removeItem(CLI_SETUP_REQUEST_KEY);
  } catch {
    // sessionStorage unavailable
  }
}

export function requestCliSetup(
  backend: BackendInfo,
  action: AIBackendSetupAction,
): CliSetupRequest {
  const request: CliSetupRequest = {
    id: makeRequestId(),
    backendName: backend.name,
    displayName: backend.displayName,
    provider: backend.provider,
    brandColor: backend.brandColor,
    action,
  };

  try {
    sessionStorage.setItem(CLI_SETUP_REQUEST_KEY, JSON.stringify(request));
  } catch {
    // sessionStorage unavailable
  }

  window.dispatchEvent(
    new CustomEvent<CliSetupRequest>(CLI_SETUP_REQUEST_EVENT, {
      detail: request,
    }),
  );

  return request;
}
