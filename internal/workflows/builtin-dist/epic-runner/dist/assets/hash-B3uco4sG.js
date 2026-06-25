//#region ../../../../../../../Users/tyson/codebase/code-agents/dynamic-workflows/flue/node_modules/.pnpm/@earendil-works+pi-ai@0.79.4_@modelcontextprotocol+sdk@1.29.0_@cfworker+json-schema@4.1.1_zod@4.4.3__ws@8.21.0_zod@4.4.3/node_modules/@earendil-works/pi-ai/dist/utils/hash.js
/** Fast deterministic hash to shorten long strings */
function shortHash(str) {
	let h1 = 3735928559;
	let h2 = 1103547991;
	for (let i = 0; i < str.length; i++) {
		const ch = str.charCodeAt(i);
		h1 = Math.imul(h1 ^ ch, 2654435761);
		h2 = Math.imul(h2 ^ ch, 1597334677);
	}
	h1 = Math.imul(h1 ^ h1 >>> 16, 2246822507) ^ Math.imul(h2 ^ h2 >>> 13, 3266489909);
	h2 = Math.imul(h2 ^ h2 >>> 16, 2246822507) ^ Math.imul(h1 ^ h1 >>> 13, 3266489909);
	return (h2 >>> 0).toString(36) + (h1 >>> 0).toString(36);
}
//#endregion
export { shortHash as t };

//# sourceMappingURL=hash-B3uco4sG.js.map