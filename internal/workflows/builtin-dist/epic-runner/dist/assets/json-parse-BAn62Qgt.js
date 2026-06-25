import { t as __commonJSMin } from "./chunk-CNf5ZN-e.js";
//#region ../../../../../../../Users/tyson/codebase/code-agents/dynamic-workflows/flue/node_modules/.pnpm/partial-json@0.1.7/node_modules/partial-json/dist/options.js
var require_options = /* @__PURE__ */ __commonJSMin(((exports) => {
	/**
	* Sometimes you don't allow every type to be partially parsed.
	* For example, you may not want a partial number because it may increase its size gradually before it's complete.
	* In this case, you can use the `Allow` object to control what types you allow to be partially parsed.
	* @module
	*/
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.Allow = exports.ALL = exports.COLLECTION = exports.ATOM = exports.SPECIAL = exports.INF = exports._INFINITY = exports.INFINITY = exports.NAN = exports.BOOL = exports.NULL = exports.OBJ = exports.ARR = exports.NUM = exports.STR = void 0;
	/**
	* allow partial strings like `"hello \u12` to be parsed as `"hello "`
	*/
	exports.STR = 1;
	/**
	* allow partial numbers like `123.` to be parsed as `123`
	*/
	exports.NUM = 2;
	/**
	* allow partial arrays like `[1, 2,` to be parsed as `[1, 2]`
	*/
	exports.ARR = 4;
	/**
	* allow partial objects like `{"a": 1, "b":` to be parsed as `{"a": 1}`
	*/
	exports.OBJ = 8;
	/**
	* allow `nu` to be parsed as `null`
	*/
	exports.NULL = 16;
	/**
	* allow `tr` to be parsed as `true`, and `fa` to be parsed as `false`
	*/
	exports.BOOL = 32;
	/**
	* allow `Na` to be parsed as `NaN`
	*/
	exports.NAN = 64;
	/**
	* allow `Inf` to be parsed as `Infinity`
	*/
	exports.INFINITY = 128;
	/**
	* allow `-Inf` to be parsed as `-Infinity`
	*/
	exports._INFINITY = 256;
	exports.INF = exports.INFINITY | exports._INFINITY;
	exports.SPECIAL = exports.NULL | exports.BOOL | exports.INF | exports.NAN;
	exports.ATOM = exports.STR | exports.NUM | exports.SPECIAL;
	exports.COLLECTION = exports.ARR | exports.OBJ;
	exports.ALL = exports.ATOM | exports.COLLECTION;
	/**
	* Control what types you allow to be partially parsed.
	* The default is to allow all types to be partially parsed, which in most casees is the best option.
	* @example
	* If you don't want to allow partial objects, you can use the following code:
	* ```ts
	* import { Allow, parse } from "partial-json";
	* parse(`[{"a": 1, "b": 2}, {"a": 3,`, Allow.ARR); // [ { a: 1, b: 2 } ]
	* ```
	* Or you can use `~` to disallow a type:
	* ```ts
	* parse(`[{"a": 1, "b": 2}, {"a": 3,`, ~Allow.OBJ); // [ { a: 1, b: 2 } ]
	* ```
	* @example
	* If you don't want to allow partial strings, you can use the following code:
	* ```ts
	* import { Allow, parse } from "partial-json";
	* parse(`["complete string", "incompl`, ~Allow.STR); // [ 'complete string' ]
	* ```
	*/
	exports.Allow = {
		STR: exports.STR,
		NUM: exports.NUM,
		ARR: exports.ARR,
		OBJ: exports.OBJ,
		NULL: exports.NULL,
		BOOL: exports.BOOL,
		NAN: exports.NAN,
		INFINITY: exports.INFINITY,
		_INFINITY: exports._INFINITY,
		INF: exports.INF,
		SPECIAL: exports.SPECIAL,
		ATOM: exports.ATOM,
		COLLECTION: exports.COLLECTION,
		ALL: exports.ALL
	};
	exports.default = exports.Allow;
}));
//#endregion
//#region ../../../../../../../Users/tyson/codebase/code-agents/dynamic-workflows/flue/node_modules/.pnpm/@earendil-works+pi-ai@0.79.4_@modelcontextprotocol+sdk@1.29.0_@cfworker+json-schema@4.1.1_zod@4.4.3__ws@8.21.0_zod@4.4.3/node_modules/@earendil-works/pi-ai/dist/utils/json-parse.js
var import_dist = (/* @__PURE__ */ __commonJSMin(((exports) => {
	var __createBinding = exports && exports.__createBinding || (Object.create ? (function(o, m, k, k2) {
		if (k2 === void 0) k2 = k;
		var desc = Object.getOwnPropertyDescriptor(m, k);
		if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) desc = {
			enumerable: true,
			get: function() {
				return m[k];
			}
		};
		Object.defineProperty(o, k2, desc);
	}) : (function(o, m, k, k2) {
		if (k2 === void 0) k2 = k;
		o[k2] = m[k];
	}));
	var __exportStar = exports && exports.__exportStar || function(m, exports$1) {
		for (var p in m) if (p !== "default" && !Object.prototype.hasOwnProperty.call(exports$1, p)) __createBinding(exports$1, m, p);
	};
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.Allow = exports.MalformedJSON = exports.PartialJSON = exports.parseJSON = exports.parse = void 0;
	var options_1 = require_options();
	Object.defineProperty(exports, "Allow", {
		enumerable: true,
		get: function() {
			return options_1.Allow;
		}
	});
	__exportStar(require_options(), exports);
	var PartialJSON = class extends Error {};
	exports.PartialJSON = PartialJSON;
	var MalformedJSON = class extends Error {};
	exports.MalformedJSON = MalformedJSON;
	/**
	* Parse incomplete JSON
	* @param {string} jsonString Partial JSON to be parsed
	* @param {number} allowPartial Specify what types are allowed to be partial, see {@link Allow} for details
	* @returns The parsed JSON
	* @throws {PartialJSON} If the JSON is incomplete (related to the `allow` parameter)
	* @throws {MalformedJSON} If the JSON is malformed
	*/
	function parseJSON(jsonString, allowPartial = options_1.Allow.ALL) {
		if (typeof jsonString !== "string") throw new TypeError(`expecting str, got ${typeof jsonString}`);
		if (!jsonString.trim()) throw new Error(`${jsonString} is empty`);
		return _parseJSON(jsonString.trim(), allowPartial);
	}
	exports.parseJSON = parseJSON;
	var _parseJSON = (jsonString, allow) => {
		const length = jsonString.length;
		let index = 0;
		const markPartialJSON = (msg) => {
			throw new PartialJSON(`${msg} at position ${index}`);
		};
		const throwMalformedError = (msg) => {
			throw new MalformedJSON(`${msg} at position ${index}`);
		};
		const parseAny = () => {
			skipBlank();
			if (index >= length) markPartialJSON("Unexpected end of input");
			if (jsonString[index] === "\"") return parseStr();
			if (jsonString[index] === "{") return parseObj();
			if (jsonString[index] === "[") return parseArr();
			if (jsonString.substring(index, index + 4) === "null" || options_1.Allow.NULL & allow && length - index < 4 && "null".startsWith(jsonString.substring(index))) {
				index += 4;
				return null;
			}
			if (jsonString.substring(index, index + 4) === "true" || options_1.Allow.BOOL & allow && length - index < 4 && "true".startsWith(jsonString.substring(index))) {
				index += 4;
				return true;
			}
			if (jsonString.substring(index, index + 5) === "false" || options_1.Allow.BOOL & allow && length - index < 5 && "false".startsWith(jsonString.substring(index))) {
				index += 5;
				return false;
			}
			if (jsonString.substring(index, index + 8) === "Infinity" || options_1.Allow.INFINITY & allow && length - index < 8 && "Infinity".startsWith(jsonString.substring(index))) {
				index += 8;
				return Infinity;
			}
			if (jsonString.substring(index, index + 9) === "-Infinity" || options_1.Allow._INFINITY & allow && 1 < length - index && length - index < 9 && "-Infinity".startsWith(jsonString.substring(index))) {
				index += 9;
				return -Infinity;
			}
			if (jsonString.substring(index, index + 3) === "NaN" || options_1.Allow.NAN & allow && length - index < 3 && "NaN".startsWith(jsonString.substring(index))) {
				index += 3;
				return NaN;
			}
			return parseNum();
		};
		const parseStr = () => {
			const start = index;
			let escape = false;
			index++;
			while (index < length && (jsonString[index] !== "\"" || escape && jsonString[index - 1] === "\\")) {
				escape = jsonString[index] === "\\" ? !escape : false;
				index++;
			}
			if (jsonString.charAt(index) == "\"") try {
				return JSON.parse(jsonString.substring(start, ++index - Number(escape)));
			} catch (e) {
				throwMalformedError(String(e));
			}
			else if (options_1.Allow.STR & allow) try {
				return JSON.parse(jsonString.substring(start, index - Number(escape)) + "\"");
			} catch (e) {
				return JSON.parse(jsonString.substring(start, jsonString.lastIndexOf("\\")) + "\"");
			}
			markPartialJSON("Unterminated string literal");
		};
		const parseObj = () => {
			index++;
			skipBlank();
			const obj = {};
			try {
				while (jsonString[index] !== "}") {
					skipBlank();
					if (index >= length && options_1.Allow.OBJ & allow) return obj;
					const key = parseStr();
					skipBlank();
					index++;
					try {
						obj[key] = parseAny();
					} catch (e) {
						if (options_1.Allow.OBJ & allow) return obj;
						else throw e;
					}
					skipBlank();
					if (jsonString[index] === ",") index++;
				}
			} catch (e) {
				if (options_1.Allow.OBJ & allow) return obj;
				else markPartialJSON("Expected '}' at end of object");
			}
			index++;
			return obj;
		};
		const parseArr = () => {
			index++;
			const arr = [];
			try {
				while (jsonString[index] !== "]") {
					arr.push(parseAny());
					skipBlank();
					if (jsonString[index] === ",") index++;
				}
			} catch (e) {
				if (options_1.Allow.ARR & allow) return arr;
				markPartialJSON("Expected ']' at end of array");
			}
			index++;
			return arr;
		};
		const parseNum = () => {
			if (index === 0) {
				if (jsonString === "-") throwMalformedError("Not sure what '-' is");
				try {
					return JSON.parse(jsonString);
				} catch (e) {
					if (options_1.Allow.NUM & allow) try {
						return JSON.parse(jsonString.substring(0, jsonString.lastIndexOf("e")));
					} catch (e) {}
					throwMalformedError(String(e));
				}
			}
			const start = index;
			if (jsonString[index] === "-") index++;
			while (jsonString[index] && ",]}".indexOf(jsonString[index]) === -1) index++;
			if (index == length && !(options_1.Allow.NUM & allow)) markPartialJSON("Unterminated number literal");
			try {
				return JSON.parse(jsonString.substring(start, index));
			} catch (e) {
				if (jsonString.substring(start, index) === "-") markPartialJSON("Not sure what '-' is");
				try {
					return JSON.parse(jsonString.substring(start, jsonString.lastIndexOf("e")));
				} catch (e) {
					throwMalformedError(String(e));
				}
			}
		};
		const skipBlank = () => {
			while (index < length && " \n\r	".includes(jsonString[index])) index++;
		};
		return parseAny();
	};
	exports.parse = parseJSON;
})))();
var VALID_JSON_ESCAPES = new Set([
	"\"",
	"\\",
	"/",
	"b",
	"f",
	"n",
	"r",
	"t",
	"u"
]);
function isControlCharacter(char) {
	const codePoint = char.codePointAt(0);
	return codePoint !== void 0 && codePoint >= 0 && codePoint <= 31;
}
function escapeControlCharacter(char) {
	switch (char) {
		case "\b": return "\\b";
		case "\f": return "\\f";
		case "\n": return "\\n";
		case "\r": return "\\r";
		case "	": return "\\t";
		default: return `\\u${char.codePointAt(0)?.toString(16).padStart(4, "0") ?? "0000"}`;
	}
}
/**
* Repairs malformed JSON string literals by:
* - escaping raw control characters inside strings
* - doubling backslashes before invalid escape characters
*/
function repairJson(json) {
	let repaired = "";
	let inString = false;
	for (let index = 0; index < json.length; index++) {
		const char = json[index];
		if (!inString) {
			repaired += char;
			if (char === "\"") inString = true;
			continue;
		}
		if (char === "\"") {
			repaired += char;
			inString = false;
			continue;
		}
		if (char === "\\") {
			const nextChar = json[index + 1];
			if (nextChar === void 0) {
				repaired += "\\\\";
				continue;
			}
			if (nextChar === "u") {
				const unicodeDigits = json.slice(index + 2, index + 6);
				if (/^[0-9a-fA-F]{4}$/.test(unicodeDigits)) {
					repaired += `\\u${unicodeDigits}`;
					index += 5;
					continue;
				}
			}
			if (VALID_JSON_ESCAPES.has(nextChar)) {
				repaired += `\\${nextChar}`;
				index += 1;
				continue;
			}
			repaired += "\\\\";
			continue;
		}
		repaired += isControlCharacter(char) ? escapeControlCharacter(char) : char;
	}
	return repaired;
}
function parseJsonWithRepair(json) {
	try {
		return JSON.parse(json);
	} catch (error) {
		const repairedJson = repairJson(json);
		if (repairedJson !== json) return JSON.parse(repairedJson);
		throw error;
	}
}
/**
* Attempts to parse potentially incomplete JSON during streaming.
* Always returns a valid object, even if the JSON is incomplete.
*
* @param partialJson The partial JSON string from streaming
* @returns Parsed object or empty object if parsing fails
*/
function parseStreamingJson(partialJson) {
	if (!partialJson || partialJson.trim() === "") return {};
	try {
		return parseJsonWithRepair(partialJson);
	} catch {
		try {
			return (0, import_dist.parse)(partialJson) ?? {};
		} catch {
			try {
				return (0, import_dist.parse)(repairJson(partialJson)) ?? {};
			} catch {
				return {};
			}
		}
	}
}
//#endregion
export { parseStreamingJson as n, parseJsonWithRepair as t };

//# sourceMappingURL=json-parse-BAn62Qgt.js.map