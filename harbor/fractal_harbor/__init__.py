"""Harbor adapter for the FRACTAL arm: plasma-fractal's recursive
self-organizing node tree driving codex on SWE-Marathon.

Usage:
    PYTHONPATH=/path/to/loomcli/harbor harbor run -p tasks/slack-clone \
        -a fractal_harbor:FractalAgent -e docker [--ak max_cost=90]

Third arm of the orchestration comparison: flat role-ensemble (loom_harbor)
vs recursive tree (this) vs single session (harbor's codex agent), all on the
same backend CLI + model.
"""

import shlex
import tempfile
from pathlib import Path
from typing import override

from harbor.agents.installed.base import BaseInstalledAgent
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext
from harbor.models.trial.paths import EnvironmentPaths

REMOTE_HOME = "/installed-agent/fractal-marathon"

_ARCH_OK = {"x86_64", "amd64", "aarch64", "arm64"}


class FractalAgent(BaseInstalledAgent):
    """plasma-fractal recursive node tree (codex backend)."""

    def __init__(
        self,
        *args,
        budget_secs: int | str = 14400,
        reserve_secs: int | str = 2400,
        max_cost: float | str = 90.0,
        max_depth: int | str = 3,
        max_children: int | str = 4,
        max_descendants: int | str = 10,
        max_iters: int | str = 60,
        mission_mode: str = "guided",
        price_alias: str | None = None,
        reserve_budget: float | str | None = None,
        fractal_pip_spec: str = "plasma-fractal==1.1.0",
        codex_npm_version: str = "0.142.5",
        codex_auth_json_path: str | None = None,
        **kwargs,
    ):
        super().__init__(*args, **kwargs)
        self._budget_secs = int(budget_secs)
        self._reserve_secs = int(reserve_secs)
        self._max_cost = float(max_cost)
        self._max_depth = int(max_depth)
        self._max_children = int(max_children)
        self._max_descendants = int(max_descendants)
        self._max_iters = int(max_iters)
        self._mission_mode = str(mission_mode)
        self._price_alias = price_alias
        self._reserve_budget = reserve_budget
        self._fractal_pip_spec = fractal_pip_spec
        self._codex_npm_version = codex_npm_version
        self._codex_auth_json_path = codex_auth_json_path

    @staticmethod
    @override
    def name() -> str:
        return "fractal-tree"

    @override
    def version(self) -> str:
        return self._version or "1.1.0"

    @property
    def _pkg_root(self) -> Path:
        return Path(__file__).resolve().parent.parent

    @override
    async def install(self, environment: BaseEnvironment) -> None:
        result = await self.exec_as_root(environment, "uname -m")
        if not any(t in (result.stdout or "") for t in _ARCH_OK):
            raise RuntimeError(f"unsupported arch: {result.stdout!r}")

        # tmux (fractal runs node loops inside tmux sessions) + fractal itself
        # into the image's /opt/venv (python 3.12 on ubuntu:24.04).
        await self.exec_as_root(
            environment,
            "apt-get update -qq && apt-get install -y -qq tmux",
            timeout_sec=300,
        )
        await self.exec_as_root(
            environment,
            f"/opt/venv/bin/pip install --quiet {shlex.quote(self._fractal_pip_spec)} "
            f"&& ln -sf /opt/venv/bin/fractal /usr/local/bin/fractal "
            f"&& fractal --help >/dev/null",
            timeout_sec=600,
        )
        await self.exec_as_root(
            environment,
            f"npm install -g @openai/codex@{shlex.quote(self._codex_npm_version)} && codex --version",
            timeout_sec=600,
        )

        auth = self._resolve_codex_auth()
        await environment.upload_file(auth, "/root/.codex/auth.json")
        await self.exec_as_root(environment, "chmod 600 /root/.codex/auth.json")

        script = self._pkg_root / "scripts" / "orchestrate-fractal.sh"
        await self.exec_as_root(environment, f"mkdir -p {REMOTE_HOME}")
        await environment.upload_file(script, f"{REMOTE_HOME}/orchestrate-fractal.sh")
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
    def get_version_command(self) -> str | None:
        return "/opt/venv/bin/pip show plasma-fractal 2>/dev/null | grep -m1 Version"

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
            "FRACTAL_HOME": REMOTE_HOME,
            "FRACTAL_BUDGET_SECS": str(self._budget_secs),
            "FRACTAL_RESERVE_SECS": str(self._reserve_secs),
            "FRACTAL_MAX_COST": str(self._max_cost),
            "FRACTAL_MAX_DEPTH": str(self._max_depth),
            "FRACTAL_MAX_CHILDREN": str(self._max_children),
            "FRACTAL_MAX_DESCENDANTS": str(self._max_descendants),
            "FRACTAL_MAX_ITERS": str(self._max_iters),
            "FRACTAL_MISSION_MODE": self._mission_mode,
        }
        if self._reserve_budget is not None:
            env["FRACTAL_RESERVE_BUDGET"] = str(self._reserve_budget)
        if self._price_alias:
            env["FRACTAL_PRICE_ALIAS"] = self._price_alias
        if self.model_name:
            env["FRACTAL_MODEL"] = self.model_name.split("/")[-1]
        await self.exec_as_agent(
            environment,
            (
                f"mkdir -p {agent_dir} && "
                f"bash {REMOTE_HOME}/orchestrate-fractal.sh 2>&1 "
                f"| tee {agent_dir}/orchestrate.log"
            ),
            env=env,
        )

    @override
    def populate_context_post_run(self, context: AgentContext) -> None:
        cost_files = sorted(self.logs_dir.glob("**/fractal-cost-final.txt"))
        if cost_files:
            context.metadata = (context.metadata or {}) | {
                "fractal_cost_report": cost_files[-1].read_text()[:2000]
            }
