import { n as __esmMin } from "./chunk-CNf5ZN-e.js";
import { l as diag, n as init_esm } from "./esm-DScMeqHs.js";
import { n as init_execAsync, t as execAsync } from "./execAsync-DD8dRiq2.js";
//#region ../../../../../../../Users/tyson/codebase/code-agents/dynamic-workflows/flue/node_modules/.pnpm/@opentelemetry+resources@2.7.1_@opentelemetry+api@1.9.1/node_modules/@opentelemetry/resources/build/esm/detectors/platform/node/machine-id/getMachineId-darwin.js
async function getMachineId() {
	try {
		const idLine = (await execAsync("ioreg -rd1 -c \"IOPlatformExpertDevice\"")).stdout.split("\n").find((line) => line.includes("IOPlatformUUID"));
		if (!idLine) return;
		const parts = idLine.split("\" = \"");
		if (parts.length === 2) return parts[1].slice(0, -1);
	} catch (e) {
		diag.debug(`error reading machine id: ${e}`);
	}
}
//#endregion
__esmMin((() => {
	init_execAsync();
	init_esm();
}))();
export { getMachineId };

//# sourceMappingURL=getMachineId-darwin-De6VsRtg.js.map