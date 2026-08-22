"""Harbor adapter that runs the loom multi-agent ensemble as a benchmark agent.

Usage:
    PYTHONPATH=/path/to/loomcli/harbor harbor run -p tasks/slack-clone \
        -a loom_harbor:LoomAgent -e docker [--ak stub=true] [--ak spend_cap_usd=60]

The agent installs a cross-compiled loom + fleet-db bundle plus the harness
scripts/prompts into /installed-agent, then runs scripts/orchestrate.sh
synchronously. orchestrate.sh owns the whole ensemble lifecycle: lead seed,
daemon (planner + coder), lead orchestrate passes, harness-invoked critic,
the atomic check-before-fast-forward integration gate into /app, and
finalization (daemon stop, port sweep, evidence dump).

Design references: ~/.claude/plans/refactored-petting-steele.md (codex-vetted R1-R4).
"""

import json
import re
import shlex
import tarfile
import tempfile
from pathlib import Path
from typing import override

from harbor.agents.installed.base import BaseInstalledAgent
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext
from harbor.models.trial.paths import EnvironmentPaths

# Everything the harness installs in the container lives under this root.
REMOTE_HOME = "/installed-agent/loom-marathon"

# uname -m -> Go arch. Fail-closed: unknown arches raise instead of guessing.
_ARCH_MAP = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}


class LoomAgent(BaseInstalledAgent):
    """Loom multi-agent ensemble (lead / planner / coder / critic on codex).

    ``team`` selects a template-backed worker team. The fullstack template has
    four runnable agents, so team mode requires ``max_agents >= 4``. ``arch``
    and ``lead_maint`` remain separate, mutually exclusive experiment knobs.
    """

    def __init__(
        self,
        *args,
        stub: bool | str = False,
        budget_secs: int | str = 14400,
        reserve_secs: int | str = 2400,
        cadence_secs: int | str = 360,
        spend_cap_usd: float | str = 90.0,
        max_agents: int | str = 2,
        codex_auth_json_path: str | None = None,
        codex_npm_version: str = "0.142.5",
        prompts_profile: str = "default",
        critic: str = "auto",
        lead_mode: str = "oneshot",
        verify_role: str = "off",
        arch: str = "off",
        lead_maint: int | str = 0,
        team: str = "off",
        backend: str | None = None,
        worker_backend: str | None = None,
        lead_backend: str | None = None,
        cursor_api_key_path: str | None = None,
        cursor_install_url: str = "https://cursor.com/install",
        **kwargs,
    ):
        super().__init__(*args, **kwargs)
        self._stub = str(stub).lower() in ("1", "true", "yes")
        self._budget_secs = int(budget_secs)
        self._reserve_secs = int(reserve_secs)
        self._cadence_secs = int(cadence_secs)
        self._spend_cap_usd = float(spend_cap_usd)
        self._max_agents = int(max_agents)
        self._codex_auth_json_path = codex_auth_json_path
        self._codex_npm_version = codex_npm_version
        self._prompts_profile = str(prompts_profile)
        self._critic = str(critic)
        self._lead_mode = str(lead_mode)
        self._verify_role = str(verify_role)
        self._arch = str(arch)
        self._lead_maint = str(lead_maint)
        self._team = str(team)
        # Backends: `backend` sets both roles; worker_backend (daemon agents:
        # architect/dev/QA) and lead_backend (every `loom lead` session —
        # persistent lead/qa/arch, oneshot passes, critic) override it. codex
        # lead = app-server runtime; cursor lead = loom's headless runtime
        # (cursor-agent -p --resume per turn). spend.sh only meters codex
        # rollouts, so with cursor roles the spend cap covers codex roles alone
        # and budget_secs is the real bound.
        default_backend = str(backend or "codex")
        self._worker_backend = str(worker_backend or default_backend)
        self._lead_backend = str(lead_backend or default_backend)
        for label, value in (("worker_backend", self._worker_backend), ("lead_backend", self._lead_backend)):
            if value not in ("codex", "cursor"):
                raise ValueError(f"{label} must be codex or cursor, got {value!r}")
        self._needs_cursor = "cursor" in (self._worker_backend, self._lead_backend)
        if self._needs_cursor and self._stub:
            raise ValueError("cursor backends have no stub; drop --ak stub=true")
        self._cursor_api_key_path = cursor_api_key_path
        self._cursor_install_url = str(cursor_install_url)
        if self._team != "off" and self._max_agents < 4:
            raise ValueError(
                "team mode requires max_agents >= 4; the fullstack bundle has "
                "4 runnable agents"
            )
        # The team arm's prompts (lead-persistent-team.md and the four
        # team-*-override.md worker prompts) live only in prompts-generic;
        # the default profile would fail bootstrap ("no override for ...").
        if self._team != "off":
            if self._prompts_profile == "default":
                self._prompts_profile = "generic"
            elif self._prompts_profile != "generic":
                raise ValueError(
                    f"team mode requires prompts_profile=generic (got {self._prompts_profile!r})"
                )

    @staticmethod
    @override
    def name() -> str:
        return "loom-ensemble"

    @override
    def version(self) -> str:
        return self._version or "dev"

    # ---------- install ----------

    @property
    def _pkg_root(self) -> Path:
        """harbor/ directory in the loomcli checkout (parent of loom_harbor/)."""
        return Path(__file__).resolve().parent.parent

    async def _detect_arch(self, environment: BaseEnvironment) -> str:
        result = await self.exec_as_root(environment, "uname -m")
        raw = (result.stdout or "").strip()
        # Executor wrappers can pollute stdout (e.g. podman's compose-provider
        # banner, merged stderr, ANSI escapes glued to the value) — strip
        # escapes, then scan tokens for a known arch, fail-closed.
        clean = re.sub(r"\x1b\[[0-9;]*[A-Za-z]", "", raw)
        for token in reversed(clean.split()):
            arch = _ARCH_MAP.get(token.strip())
            if arch:
                return arch
        raise RuntimeError(
            f"unsupported container arch {raw!r}; bundle supports {sorted(set(_ARCH_MAP.values()))}"
        )

    async def _upload_and_extract(
        self, environment: BaseEnvironment, local_tar: Path, remote_dir: str
    ) -> None:
        remote_tar = f"/tmp/{local_tar.name}"
        await environment.upload_file(local_tar, remote_tar)
        await self.exec_as_root(
            environment,
            f"mkdir -p {shlex.quote(remote_dir)} && "
            f"tar -xzf {shlex.quote(remote_tar)} -C {shlex.quote(remote_dir)} && "
            f"rm -f {shlex.quote(remote_tar)}",
        )

    def _make_payload_tar(self) -> Path:
        """Tar scripts/ prompts/ stub/ from the repo into a temp file."""
        tmp = tempfile.NamedTemporaryFile(
            suffix=".tar.gz", prefix="loom-marathon-payload-", delete=False
        )
        with tarfile.open(tmp.name, "w:gz") as tf:
            for sub in ("scripts", "prompts", "prompts-generic", "stub"):
                src = self._pkg_root / sub
                if src.is_dir():
                    tf.add(src, arcname=sub)
        return Path(tmp.name)

    @override
    async def install(self, environment: BaseEnvironment) -> None:
        arch = await self._detect_arch(environment)
        bundle = self._pkg_root / "bundle" / "dist" / f"loom-bundle-linux-{arch}.tar.gz"
        if not bundle.is_file():
            raise RuntimeError(
                f"bundle missing: {bundle}. Run harbor/bundle/build-bundle.sh first."
            )
        # loom + fleet-db binaries -> $REMOTE_HOME/bin (fleet-db resolves as sibling of loom;
        # LOOM_FLEET_DB_BIN is also set explicitly by bootstrap).
        await self._upload_and_extract(environment, bundle, REMOTE_HOME)

        payload = self._make_payload_tar()
        try:
            await self._upload_and_extract(environment, payload, REMOTE_HOME)
        finally:
            payload.unlink(missing_ok=True)

        await self.exec_as_root(
            environment,
            f"chmod -R a+rX {REMOTE_HOME} && chmod +x {REMOTE_HOME}/bin/* "
            f"&& if [ -d {REMOTE_HOME}/stub ]; then chmod +x {REMOTE_HOME}/stub/*; fi",
        )

        if self._stub:
            # Fake backend: stubbin/codex first on PATH, no npm install, no auth.
            await self.exec_as_root(
                environment,
                f"mkdir -p {REMOTE_HOME}/stubbin && "
                f"cp {REMOTE_HOME}/stub/codex {REMOTE_HOME}/stubbin/codex && "
                f"chmod +x {REMOTE_HOME}/stubbin/codex",
            )
        else:
            # Real codex CLI. The task image ships node >= 22 (slack-clone needs npm).
            await self.exec_as_root(
                environment,
                f"npm install -g @openai/codex@{shlex.quote(self._codex_npm_version)} "
                f"&& codex --version",
                timeout_sec=600,
            )
            if self._lead_mode == "persistent":
                # Controlled lead runtime = codex TUI on a tmux pty (same
                # approach the fractal arm proved in this image family).
                await self.exec_as_root(
                    environment,
                    "command -v tmux >/dev/null 2>&1 || "
                    "(apt-get update -qq && apt-get install -y -qq tmux)",
                    timeout_sec=300,
                )
            auth_path = self._resolve_codex_auth()
            remote_auth_dir = "/installed-agent/codex-auth"
            await environment.upload_file(auth_path, f"{remote_auth_dir}/auth.json")
            chown_user = environment.default_user
            chown = (
                f"chown -R {chown_user} {remote_auth_dir} && " if chown_user else ""
            )
            await self.exec_as_root(
                environment, f"{chown}chmod 600 {remote_auth_dir}/auth.json"
            )
            if self._needs_cursor:
                await self._install_cursor(environment)

        if environment.default_user:
            await self.exec_as_root(
                environment,
                f"chown -R {environment.default_user} /installed-agent",
            )

    async def _install_cursor(self, environment: BaseEnvironment) -> None:
        """cursor-agent for cursor roles: official installer into a
        container-only HOME (verified on ubuntu:24.04 arm64), symlinked onto
        PATH; API key uploaded as a 600 file that bootstrap exports lazily as
        CURSOR_API_KEY (the key itself never lands in the host trial dir)."""
        cursor_home = "/installed-agent/cursor-home"
        await self.exec_as_root(
            environment,
            "command -v curl >/dev/null 2>&1 || "
            "(apt-get update -qq && apt-get install -y -qq curl ca-certificates) && "
            f"mkdir -p {cursor_home} && "
            f"HOME={cursor_home} bash -c "
            f"'curl -fsSL {shlex.quote(self._cursor_install_url)} | bash' && "
            f"ln -sf {cursor_home}/.local/bin/cursor-agent /usr/local/bin/cursor-agent && "
            f"chmod -R a+rX {cursor_home} && cursor-agent --version",
            timeout_sec=600,
        )
        key_path = self._resolve_cursor_api_key()
        remote_dir = "/installed-agent/cursor-auth"
        await environment.upload_file(key_path, f"{remote_dir}/api-key")
        chown_user = environment.default_user
        chown = f"chown -R {chown_user} {remote_dir} && " if chown_user else ""
        await self.exec_as_root(environment, f"{chown}chmod 600 {remote_dir}/api-key")

    def _resolve_cursor_api_key(self) -> str:
        """Path of a file holding ONE Cursor API key (cursor.com/dashboard ->
        Integrations -> User API Keys). Only existence/size is checked here;
        the contents are never read by the adapter."""
        candidate = self._cursor_api_key_path or self._get_env(
            "LOOM_MARATHON_CURSOR_API_KEY_FILE"
        )
        if not candidate:
            candidate = str(Path.home() / ".cursor" / "marathon-api-key")
        p = Path(candidate).expanduser()
        if not p.is_file() or p.stat().st_size == 0:
            raise RuntimeError(
                f"cursor API key file not found or empty at {p}; pass "
                "--ak cursor_api_key_path=... or set LOOM_MARATHON_CURSOR_API_KEY_FILE "
                "(cursor backends need a Cursor user API key in a file)"
            )
        return str(p)

    def _resolve_codex_auth(self) -> str:
        """Sanitized codex credential: auth.json ONLY (never host config/history)."""
        candidate = self._codex_auth_json_path or self._get_env(
            "LOOM_MARATHON_CODEX_AUTH"
        )
        if not candidate:
            candidate = str(Path.home() / ".codex" / "auth.json")
        p = Path(candidate).expanduser()
        if not p.is_file():
            raise RuntimeError(
                f"codex auth.json not found at {p}; pass --ak codex_auth_json_path=... "
                "or set LOOM_MARATHON_CODEX_AUTH (real mode needs codex credentials; "
                "use --ak stub=true for the free dry-run)"
            )
        return str(p)

    @override
    def get_version_command(self) -> str | None:
        return f"cat {REMOTE_HOME}/bin/VERSION 2>/dev/null | head -1"

    # ---------- run ----------

    def _run_env(self) -> dict[str, str]:
        env = {
            "LOOM_MARATHON_HOME": REMOTE_HOME,
            "LOOM_MARATHON_BUDGET_SECS": str(self._budget_secs),
            "LOOM_MARATHON_RESERVE_SECS": str(self._reserve_secs),
            "LOOM_MARATHON_CADENCE_SECS": str(self._cadence_secs),
            "LOOM_MARATHON_SPEND_CAP_USD": str(self._spend_cap_usd),
            "LOOM_MARATHON_MAX_AGENTS": str(self._max_agents),
        }
        if self._prompts_profile == "generic":
            env["LOOM_MARATHON_PROMPTS_DIR"] = f"{REMOTE_HOME}/prompts-generic"
        if self._critic != "auto":
            env["LOOM_MARATHON_CRITIC"] = self._critic
        if self._lead_mode != "oneshot":
            env["LOOM_MARATHON_LEAD_MODE"] = self._lead_mode
        if self._verify_role != "off":
            env["LOOM_MARATHON_VERIFY_ROLE"] = self._verify_role
        if self._arch != "off":
            env["LOOM_MARATHON_ARCH"] = self._arch
        if self._lead_maint not in ("0", "", "off"):
            env["LOOM_MARATHON_LEAD_MAINT"] = "1"
        if self._team != "off":
            env["LOOM_MARATHON_TEAM"] = self._team
        if self._worker_backend != "codex":
            env["LOOM_MARATHON_WORKER_BACKEND"] = self._worker_backend
        if self._lead_backend != "codex":
            env["LOOM_MARATHON_LEAD_BACKEND"] = self._lead_backend
        if self._stub:
            env["LOOM_MARATHON_STUB"] = "1"
        if self.model_name:
            env["LOOM_MARATHON_MODEL"] = self.model_name.split("/")[-1]
        return env

    @override
    async def run(
        self, instruction: str, environment: BaseEnvironment, context: AgentContext
    ) -> None:
        # Instruction goes up as a file: no shell-quoting limits, exact bytes.
        with tempfile.NamedTemporaryFile(
            "w", suffix=".md", prefix="instruction-", delete=False
        ) as f:
            f.write(instruction)
            local_instruction = Path(f.name)
        try:
            await environment.upload_file(
                local_instruction, f"{REMOTE_HOME}/instruction.md"
            )
        finally:
            local_instruction.unlink(missing_ok=True)
        if environment.default_user:
            await self.exec_as_root(
                environment,
                f"chown {environment.default_user} {REMOTE_HOME}/instruction.md",
            )

        agent_dir = EnvironmentPaths.agent_dir.as_posix()
        # Synchronous, single blocking exec. orchestrate.sh self-finalizes inside
        # budget_secs - reserve_secs; Harbor's agent timeout is the outer guard.
        # Only Harbor's own AgentTimeoutError / NonZeroAgentExitCodeError escape.
        await self.exec_as_agent(
            environment,
            (
                f"mkdir -p {agent_dir} && "
                f"bash {REMOTE_HOME}/scripts/orchestrate.sh 2>&1 "
                f"| tee {agent_dir}/orchestrate.log"
            ),
            env=self._run_env(),
        )

    @override
    def populate_context_post_run(self, context: AgentContext) -> None:
        """Backfill token/cost fields from the synced usage summary."""
        summaries = sorted(self.logs_dir.glob("**/usage-summary.json"))
        if not summaries:
            return
        try:
            data = json.loads(summaries[-1].read_text())
        except (OSError, json.JSONDecodeError):
            return
        context.n_input_tokens = data.get("input_tokens")
        context.n_output_tokens = data.get("output_tokens")
        context.cost_usd = data.get("est_cost_usd")
        context.metadata = (context.metadata or {}) | {
            "loom_marathon": {
                k: data.get(k)
                for k in (
                    "tasks_integrated",
                    "tasks_seeded",
                    "integration_failures",
                    "finalize_reason",
                    "stub",
                    "template_id",
                    "design_auto_approved",
                    "orphans_routed",
                    "integrations_stale",
                )
                if k in data
            }
        }
