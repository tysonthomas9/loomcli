import { a as init_esm, b as __esmMin, o as diag } from "../server.mjs";
import { promises } from "fs";
//#region ../../../../../../../Users/tyson/codebase/code-agents/dynamic-workflows/flue/node_modules/.pnpm/@opentelemetry+resources@2.7.1_@opentelemetry+api@1.9.1/node_modules/@opentelemetry/resources/build/esm/detectors/platform/node/machine-id/getMachineId-linux.js
async function getMachineId() {
	for (const path of ["/etc/machine-id", "/var/lib/dbus/machine-id"]) try {
		return (await promises.readFile(path, { encoding: "utf8" })).trim();
	} catch (e) {
		diag.debug(`error reading machine id: ${e}`);
	}
}
//#endregion
__esmMin((() => {
	init_esm();
}))();
export { getMachineId };

//# sourceMappingURL=getMachineId-linux-DJMbWkLq.js.map