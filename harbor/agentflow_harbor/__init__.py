"""Harbor adapter for the AGENTFLOW-LEAD arm (EXPERIMENTS.md B4a):
a codex lead session generates and drives agentflow-go pipelines.

Usage:
    PYTHONPATH=/path/to/loomcli/harbor harbor run -p tasks/slack-clone \
        -a agentflow_harbor:AgentflowAgent -m gpt-5.5 -e docker \
        --ak spend_cap_usd=200
"""

import json
import shlex
import tempfile
from pathlib import Path
from typing import override

from harbor.agents.installed.base import BaseInstalledAgent
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext
from harbor.models.trial.paths import EnvironmentPaths

REMOTE_HOME = "/installed-agent/agentflow-marathon"
_ARCH_MAP = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}


class AgentflowAgent(BaseInstalledAgent):
    """agentflow-go fixed-DAG engine driven by a codex lead (plan-and-replan)."""

    def __init__(
        self,
        *args,
        budget_secs: int | str = 14400,
        reserve_secs: int | str = 2400,
        spend_cap_usd: float | str = 200.0,
        max_relaunch: int | str = 20,
        codex_npm_version: str = "0.142.5",
        codex_auth_json_path: str | None = None,
        **kwargs,
    ):
        super().__init__(*args, **kwargs)
        self._budget_secs = int(budget_secs)
        self._reserve_secs = int(reserve_secs)
        self._spend_cap_usd = float(spend_cap_usd)
        self._max_relaunch = int(max_relaunch)
        self._codex_npm_version = codex_npm_version
        self._codex_auth_json_path = codex_auth_json_path

    @staticmethod
    @override
    def name() -> str:
        return "agentflow-lead"

    @override
    def version(self) -> str:
        return self._version or "92ed64c"

    @property
    def _pkg_root(self) -> Path:
        return Path(__file__).resolve().parent.parent

    @override
    async def install(self, environment: BaseEnvironment) -> None:
        result = await self.exec_as_root(environment, "uname -m")
        arch = None
        for token in reversed((result.stdout or "").split()):
            arch = _ARCH_MAP.get(token.strip())
            if arch:
                break
        if not arch:
            raise RuntimeError(f"unsupported arch: {result.stdout!r}")

        binary = self._pkg_root / "bundle" / "dist" / f"agentflow-linux-{arch}"
        if not binary.is_file():
            raise RuntimeError(f"agentflow binary missing: {binary}")
        await environment.upload_file(binary, "/usr/local/bin/agentflow")
        await self.exec_as_root(
            environment, "chmod +x /usr/local/bin/agentflow && agentflow --help >/dev/null"
        )

        await self.exec_as_root(
            environment,
            f"npm install -g @openai/codex@{shlex.quote(self._codex_npm_version)} && codex --version",
            timeout_sec=600,
        )

        auth = self._resolve_codex_auth()
        await environment.upload_file(auth, "/installed-agent/codex-auth/auth.json")
        await self.exec_as_root(
            environment, "chmod 600 /installed-agent/codex-auth/auth.json"
        )

        await self.exec_as_root(environment, f"mkdir -p {REMOTE_HOME}")
        for name, remote in (
            ("scripts/orchestrate-agentflow.sh", "orchestrate-agentflow.sh"),
            ("scripts/agentflow-tool-reference.md", "tool-reference.md"),
            ("scripts/spend.sh", "spend.sh"),
        ):
            await environment.upload_file(
                self._pkg_root / name, f"{REMOTE_HOME}/{remote}"
            )
        await self.exec_as_root(
            environment, f"chmod -R a+rX {REMOTE_HOME} && chmod +x {REMOTE_HOME}/*.sh"
        )

    def _resolve_codex_auth(self) -> str:
        candidate = self._codex_auth_json_path or self._get_env(
            "LOOM_MARATHON_CODEX_AUTH"
        )
        if not candidate:
            candidate = str(Path.home() / ".codex" / "auth.json")
        p = Path(candidate).expanduser()
        if not p.is_file():
            raise RuntimeError(f"codex auth.json not found at {p}")
        return str(p)

    @override
    async def run(
        self, instruction: str, environment: BaseEnvironment, context: AgentContext
    ) -> None:
        with tempfile.NamedTemporaryFile("w", suffix=".md", delete=False) as f:
            f.write(instruction)
            local = Path(f.name)
        try:
            await environment.upload_file(local, f"{REMOTE_HOME}/instruction.md")
        finally:
            local.unlink(missing_ok=True)

        agent_dir = EnvironmentPaths.agent_dir.as_posix()
        env = {
            "AGENTFLOW_HOME": REMOTE_HOME,
            "AGENTFLOW_BUDGET_SECS": str(self._budget_secs),
            "AGENTFLOW_RESERVE_SECS": str(self._reserve_secs),
            "AGENTFLOW_SPEND_CAP_USD": str(self._spend_cap_usd),
            "AGENTFLOW_MAX_RELAUNCH": str(self._max_relaunch),
        }
        if self.model_name:
            env["AGENTFLOW_MODEL"] = self.model_name.split("/")[-1]
        await self.exec_as_agent(
            environment,
            (
                f"mkdir -p {agent_dir} && "
                f"bash {REMOTE_HOME}/orchestrate-agentflow.sh 2>&1 "
                f"| tee {agent_dir}/orchestrate.log"
            ),
            env=env,
        )

    @override
    def populate_context_post_run(self, context: AgentContext) -> None:
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
            "agentflow": {k: data.get(k) for k in ("finalize_reason", "relaunches")}
        }
