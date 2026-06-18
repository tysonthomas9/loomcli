import { a as init_esm, b as __esmMin, o as diag } from "../server.mjs";
import { n as init_execAsync, t as execAsync } from "./execAsync-DqjLTHLZ.js";
import { promises } from "fs";
//#region ../../../../../../../Users/tyson/codebase/code-agents/dynamic-workflows/flue/node_modules/.pnpm/@opentelemetry+resources@2.7.1_@opentelemetry+api@1.9.1/node_modules/@opentelemetry/resources/build/esm/detectors/platform/node/machine-id/getMachineId-bsd.js
async function getMachineId() {
	try {
		return (await promises.readFile("/etc/hostid", { encoding: "utf8" })).trim();
	} catch (e) {
		diag.debug(`error reading machine id: ${e}`);
	}
	try {
		return (await execAsync("kenv -q smbios.system.uuid")).stdout.trim();
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

//# sourceMappingURL=getMachineId-bsd-DwIzMCHM.js.map