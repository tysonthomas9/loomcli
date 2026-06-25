import { n as __esmMin } from "./chunk-CNf5ZN-e.js";
import { l as diag, n as init_esm } from "./esm-DScMeqHs.js";
import { n as init_execAsync, t as execAsync } from "./execAsync-DD8dRiq2.js";
import * as process from "process";
//#region ../../../../../../../Users/tyson/codebase/code-agents/dynamic-workflows/flue/node_modules/.pnpm/@opentelemetry+resources@2.7.1_@opentelemetry+api@1.9.1/node_modules/@opentelemetry/resources/build/esm/detectors/platform/node/machine-id/getMachineId-win.js
async function getMachineId() {
	const args = "QUERY HKEY_LOCAL_MACHINE\\SOFTWARE\\Microsoft\\Cryptography /v MachineGuid";
	let command = "%windir%\\System32\\REG.exe";
	if (process.arch === "ia32" && "PROCESSOR_ARCHITEW6432" in process.env) command = "%windir%\\sysnative\\cmd.exe /c " + command;
	try {
		const parts = (await execAsync(`${command} ${args}`)).stdout.split("REG_SZ");
		if (parts.length === 2) return parts[1].trim();
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

//# sourceMappingURL=getMachineId-win-CGTqlXQp.js.map