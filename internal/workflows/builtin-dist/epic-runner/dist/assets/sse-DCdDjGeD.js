import { i as require_types, n as require_auth, r as require_transport, t as require_dist$1, y as __commonJSMin } from "../server.mjs";
//#region ../flue/node_modules/.pnpm/eventsource@3.0.7/node_modules/eventsource/dist/index.cjs
var require_dist = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: !0 });
	var eventsourceParser = require_dist$1();
	var ErrorEvent = class extends Event {
		/**
		* Constructs a new `ErrorEvent` instance. This is typically not called directly,
		* but rather emitted by the `EventSource` object when an error occurs.
		*
		* @param type - The type of the event (should be "error")
		* @param errorEventInitDict - Optional properties to include in the error event
		*/
		constructor(type, errorEventInitDict) {
			var _a, _b;
			super(type), this.code = (_a = errorEventInitDict == null ? void 0 : errorEventInitDict.code) != null ? _a : void 0, this.message = (_b = errorEventInitDict == null ? void 0 : errorEventInitDict.message) != null ? _b : void 0;
		}
		/**
		* Node.js "hides" the `message` and `code` properties of the `ErrorEvent` instance,
		* when it is `console.log`'ed. This makes it harder to debug errors. To ease debugging,
		* we explicitly include the properties in the `inspect` method.
		*
		* This is automatically called by Node.js when you `console.log` an instance of this class.
		*
		* @param _depth - The current depth
		* @param options - The options passed to `util.inspect`
		* @param inspect - The inspect function to use (prevents having to import it from `util`)
		* @returns A string representation of the error
		*/
		[Symbol.for("nodejs.util.inspect.custom")](_depth, options, inspect) {
			return inspect(inspectableError(this), options);
		}
		/**
		* Deno "hides" the `message` and `code` properties of the `ErrorEvent` instance,
		* when it is `console.log`'ed. This makes it harder to debug errors. To ease debugging,
		* we explicitly include the properties in the `inspect` method.
		*
		* This is automatically called by Deno when you `console.log` an instance of this class.
		*
		* @param inspect - The inspect function to use (prevents having to import it from `util`)
		* @param options - The options passed to `Deno.inspect`
		* @returns A string representation of the error
		*/
		[Symbol.for("Deno.customInspect")](inspect, options) {
			return inspect(inspectableError(this), options);
		}
	};
	function syntaxError(message) {
		const DomException = globalThis.DOMException;
		return typeof DomException == "function" ? new DomException(message, "SyntaxError") : new SyntaxError(message);
	}
	function flattenError(err) {
		return err instanceof Error ? "errors" in err && Array.isArray(err.errors) ? err.errors.map(flattenError).join(", ") : "cause" in err && err.cause instanceof Error ? `${err}: ${flattenError(err.cause)}` : err.message : `${err}`;
	}
	function inspectableError(err) {
		return {
			type: err.type,
			message: err.message,
			code: err.code,
			defaultPrevented: err.defaultPrevented,
			cancelable: err.cancelable,
			timeStamp: err.timeStamp
		};
	}
	var __typeError = (msg) => {
		throw TypeError(msg);
	}, __accessCheck = (obj, member, msg) => member.has(obj) || __typeError("Cannot " + msg), __privateGet = (obj, member, getter) => (__accessCheck(obj, member, "read from private field"), getter ? getter.call(obj) : member.get(obj)), __privateAdd = (obj, member, value) => member.has(obj) ? __typeError("Cannot add the same private member more than once") : member instanceof WeakSet ? member.add(obj) : member.set(obj, value), __privateSet = (obj, member, value, setter) => (__accessCheck(obj, member, "write to private field"), member.set(obj, value), value), __privateMethod = (obj, member, method) => (__accessCheck(obj, member, "access private method"), method), _readyState, _url, _redirectUrl, _withCredentials, _fetch, _reconnectInterval, _reconnectTimer, _lastEventId, _controller, _parser, _onError, _onMessage, _onOpen, _EventSource_instances, connect_fn, _onFetchResponse, _onFetchError, getRequestOptions_fn, _onEvent, _onRetryChange, failConnection_fn, scheduleReconnect_fn, _reconnect;
	var EventSource = class extends EventTarget {
		constructor(url, eventSourceInitDict) {
			var _a, _b;
			super(), __privateAdd(this, _EventSource_instances), this.CONNECTING = 0, this.OPEN = 1, this.CLOSED = 2, __privateAdd(this, _readyState), __privateAdd(this, _url), __privateAdd(this, _redirectUrl), __privateAdd(this, _withCredentials), __privateAdd(this, _fetch), __privateAdd(this, _reconnectInterval), __privateAdd(this, _reconnectTimer), __privateAdd(this, _lastEventId, null), __privateAdd(this, _controller), __privateAdd(this, _parser), __privateAdd(this, _onError, null), __privateAdd(this, _onMessage, null), __privateAdd(this, _onOpen, null), __privateAdd(this, _onFetchResponse, async (response) => {
				var _a2;
				__privateGet(this, _parser).reset();
				const { body, redirected, status, headers } = response;
				if (status === 204) {
					__privateMethod(this, _EventSource_instances, failConnection_fn).call(this, "Server sent HTTP 204, not reconnecting", 204), this.close();
					return;
				}
				if (redirected ? __privateSet(this, _redirectUrl, new URL(response.url)) : __privateSet(this, _redirectUrl, void 0), status !== 200) {
					__privateMethod(this, _EventSource_instances, failConnection_fn).call(this, `Non-200 status code (${status})`, status);
					return;
				}
				if (!(headers.get("content-type") || "").startsWith("text/event-stream")) {
					__privateMethod(this, _EventSource_instances, failConnection_fn).call(this, "Invalid content type, expected \"text/event-stream\"", status);
					return;
				}
				if (__privateGet(this, _readyState) === this.CLOSED) return;
				__privateSet(this, _readyState, this.OPEN);
				const openEvent = new Event("open");
				if ((_a2 = __privateGet(this, _onOpen)) == null || _a2.call(this, openEvent), this.dispatchEvent(openEvent), typeof body != "object" || !body || !("getReader" in body)) {
					__privateMethod(this, _EventSource_instances, failConnection_fn).call(this, "Invalid response body, expected a web ReadableStream", status), this.close();
					return;
				}
				const decoder = new TextDecoder(), reader = body.getReader();
				let open = !0;
				do {
					const { done, value } = await reader.read();
					value && __privateGet(this, _parser).feed(decoder.decode(value, { stream: !done })), done && (open = !1, __privateGet(this, _parser).reset(), __privateMethod(this, _EventSource_instances, scheduleReconnect_fn).call(this));
				} while (open);
			}), __privateAdd(this, _onFetchError, (err) => {
				__privateSet(this, _controller, void 0), !(err.name === "AbortError" || err.type === "aborted") && __privateMethod(this, _EventSource_instances, scheduleReconnect_fn).call(this, flattenError(err));
			}), __privateAdd(this, _onEvent, (event) => {
				typeof event.id == "string" && __privateSet(this, _lastEventId, event.id);
				const messageEvent = new MessageEvent(event.event || "message", {
					data: event.data,
					origin: __privateGet(this, _redirectUrl) ? __privateGet(this, _redirectUrl).origin : __privateGet(this, _url).origin,
					lastEventId: event.id || ""
				});
				__privateGet(this, _onMessage) && (!event.event || event.event === "message") && __privateGet(this, _onMessage).call(this, messageEvent), this.dispatchEvent(messageEvent);
			}), __privateAdd(this, _onRetryChange, (value) => {
				__privateSet(this, _reconnectInterval, value);
			}), __privateAdd(this, _reconnect, () => {
				__privateSet(this, _reconnectTimer, void 0), __privateGet(this, _readyState) === this.CONNECTING && __privateMethod(this, _EventSource_instances, connect_fn).call(this);
			});
			try {
				if (url instanceof URL) __privateSet(this, _url, url);
				else if (typeof url == "string") __privateSet(this, _url, new URL(url, getBaseURL()));
				else throw new Error("Invalid URL");
			} catch {
				throw syntaxError("An invalid or illegal string was specified");
			}
			__privateSet(this, _parser, eventsourceParser.createParser({
				onEvent: __privateGet(this, _onEvent),
				onRetry: __privateGet(this, _onRetryChange)
			})), __privateSet(this, _readyState, this.CONNECTING), __privateSet(this, _reconnectInterval, 3e3), __privateSet(this, _fetch, (_a = eventSourceInitDict == null ? void 0 : eventSourceInitDict.fetch) != null ? _a : globalThis.fetch), __privateSet(this, _withCredentials, (_b = eventSourceInitDict == null ? void 0 : eventSourceInitDict.withCredentials) != null ? _b : !1), __privateMethod(this, _EventSource_instances, connect_fn).call(this);
		}
		/**
		* Returns the state of this EventSource object's connection. It can have the values described below.
		*
		* [MDN Reference](https://developer.mozilla.org/docs/Web/API/EventSource/readyState)
		*
		* Note: typed as `number` instead of `0 | 1 | 2` for compatibility with the `EventSource` interface,
		* defined in the TypeScript `dom` library.
		*
		* @public
		*/
		get readyState() {
			return __privateGet(this, _readyState);
		}
		/**
		* Returns the URL providing the event stream.
		*
		* [MDN Reference](https://developer.mozilla.org/docs/Web/API/EventSource/url)
		*
		* @public
		*/
		get url() {
			return __privateGet(this, _url).href;
		}
		/**
		* Returns true if the credentials mode for connection requests to the URL providing the event stream is set to "include", and false otherwise.
		*
		* [MDN Reference](https://developer.mozilla.org/docs/Web/API/EventSource/withCredentials)
		*/
		get withCredentials() {
			return __privateGet(this, _withCredentials);
		}
		/** [MDN Reference](https://developer.mozilla.org/docs/Web/API/EventSource/error_event) */
		get onerror() {
			return __privateGet(this, _onError);
		}
		set onerror(value) {
			__privateSet(this, _onError, value);
		}
		/** [MDN Reference](https://developer.mozilla.org/docs/Web/API/EventSource/message_event) */
		get onmessage() {
			return __privateGet(this, _onMessage);
		}
		set onmessage(value) {
			__privateSet(this, _onMessage, value);
		}
		/** [MDN Reference](https://developer.mozilla.org/docs/Web/API/EventSource/open_event) */
		get onopen() {
			return __privateGet(this, _onOpen);
		}
		set onopen(value) {
			__privateSet(this, _onOpen, value);
		}
		addEventListener(type, listener, options) {
			const listen = listener;
			super.addEventListener(type, listen, options);
		}
		removeEventListener(type, listener, options) {
			const listen = listener;
			super.removeEventListener(type, listen, options);
		}
		/**
		* Aborts any instances of the fetch algorithm started for this EventSource object, and sets the readyState attribute to CLOSED.
		*
		* [MDN Reference](https://developer.mozilla.org/docs/Web/API/EventSource/close)
		*
		* @public
		*/
		close() {
			__privateGet(this, _reconnectTimer) && clearTimeout(__privateGet(this, _reconnectTimer)), __privateGet(this, _readyState) !== this.CLOSED && (__privateGet(this, _controller) && __privateGet(this, _controller).abort(), __privateSet(this, _readyState, this.CLOSED), __privateSet(this, _controller, void 0));
		}
	};
	_readyState = /* @__PURE__ */ new WeakMap(), _url = /* @__PURE__ */ new WeakMap(), _redirectUrl = /* @__PURE__ */ new WeakMap(), _withCredentials = /* @__PURE__ */ new WeakMap(), _fetch = /* @__PURE__ */ new WeakMap(), _reconnectInterval = /* @__PURE__ */ new WeakMap(), _reconnectTimer = /* @__PURE__ */ new WeakMap(), _lastEventId = /* @__PURE__ */ new WeakMap(), _controller = /* @__PURE__ */ new WeakMap(), _parser = /* @__PURE__ */ new WeakMap(), _onError = /* @__PURE__ */ new WeakMap(), _onMessage = /* @__PURE__ */ new WeakMap(), _onOpen = /* @__PURE__ */ new WeakMap(), _EventSource_instances = /* @__PURE__ */ new WeakSet(), connect_fn = function() {
		__privateSet(this, _readyState, this.CONNECTING), __privateSet(this, _controller, new AbortController()), __privateGet(this, _fetch)(__privateGet(this, _url), __privateMethod(this, _EventSource_instances, getRequestOptions_fn).call(this)).then(__privateGet(this, _onFetchResponse)).catch(__privateGet(this, _onFetchError));
	}, _onFetchResponse = /* @__PURE__ */ new WeakMap(), _onFetchError = /* @__PURE__ */ new WeakMap(), getRequestOptions_fn = function() {
		var _a;
		const init = {
			mode: "cors",
			redirect: "follow",
			headers: {
				Accept: "text/event-stream",
				...__privateGet(this, _lastEventId) ? { "Last-Event-ID": __privateGet(this, _lastEventId) } : void 0
			},
			cache: "no-store",
			signal: (_a = __privateGet(this, _controller)) == null ? void 0 : _a.signal
		};
		return "window" in globalThis && (init.credentials = this.withCredentials ? "include" : "same-origin"), init;
	}, _onEvent = /* @__PURE__ */ new WeakMap(), _onRetryChange = /* @__PURE__ */ new WeakMap(), failConnection_fn = function(message, code) {
		var _a;
		__privateGet(this, _readyState) !== this.CLOSED && __privateSet(this, _readyState, this.CLOSED);
		const errorEvent = new ErrorEvent("error", {
			code,
			message
		});
		(_a = __privateGet(this, _onError)) == null || _a.call(this, errorEvent), this.dispatchEvent(errorEvent);
	}, scheduleReconnect_fn = function(message, code) {
		var _a;
		if (__privateGet(this, _readyState) === this.CLOSED) return;
		__privateSet(this, _readyState, this.CONNECTING);
		const errorEvent = new ErrorEvent("error", {
			code,
			message
		});
		(_a = __privateGet(this, _onError)) == null || _a.call(this, errorEvent), this.dispatchEvent(errorEvent), __privateSet(this, _reconnectTimer, setTimeout(__privateGet(this, _reconnect), __privateGet(this, _reconnectInterval)));
	}, _reconnect = /* @__PURE__ */ new WeakMap(), EventSource.CONNECTING = 0, EventSource.OPEN = 1, EventSource.CLOSED = 2;
	function getBaseURL() {
		const doc = "document" in globalThis ? globalThis.document : void 0;
		return doc && typeof doc == "object" && "baseURI" in doc && typeof doc.baseURI == "string" ? doc.baseURI : void 0;
	}
	exports.ErrorEvent = ErrorEvent;
	exports.EventSource = EventSource;
}));
//#endregion
//#region ../flue/node_modules/.pnpm/@modelcontextprotocol+sdk@1.29.0_@cfworker+json-schema@4.1.1_zod@4.4.3/node_modules/@modelcontextprotocol/sdk/dist/cjs/client/sse.js
var require_sse = /* @__PURE__ */ __commonJSMin(((exports) => {
	Object.defineProperty(exports, "__esModule", { value: true });
	exports.SSEClientTransport = exports.SseError = void 0;
	var eventsource_1 = require_dist();
	var transport_js_1 = require_transport();
	var types_js_1 = require_types();
	var auth_js_1 = require_auth();
	var SseError = class extends Error {
		constructor(code, message, event) {
			super(`SSE error: ${message}`);
			this.code = code;
			this.event = event;
		}
	};
	exports.SseError = SseError;
	/**
	* Client transport for SSE: this will connect to a server using Server-Sent Events for receiving
	* messages and make separate POST requests for sending messages.
	* @deprecated SSEClientTransport is deprecated. Prefer to use StreamableHTTPClientTransport where possible instead. Note that because some servers are still using SSE, clients may need to support both transports during the migration period.
	*/
	var SSEClientTransport = class {
		constructor(url, opts) {
			this._url = url;
			this._resourceMetadataUrl = void 0;
			this._scope = void 0;
			this._eventSourceInit = opts?.eventSourceInit;
			this._requestInit = opts?.requestInit;
			this._authProvider = opts?.authProvider;
			this._fetch = opts?.fetch;
			this._fetchWithInit = (0, transport_js_1.createFetchWithInit)(opts?.fetch, opts?.requestInit);
		}
		async _authThenStart() {
			if (!this._authProvider) throw new auth_js_1.UnauthorizedError("No auth provider");
			let result;
			try {
				result = await (0, auth_js_1.auth)(this._authProvider, {
					serverUrl: this._url,
					resourceMetadataUrl: this._resourceMetadataUrl,
					scope: this._scope,
					fetchFn: this._fetchWithInit
				});
			} catch (error) {
				this.onerror?.(error);
				throw error;
			}
			if (result !== "AUTHORIZED") throw new auth_js_1.UnauthorizedError();
			return await this._startOrAuth();
		}
		async _commonHeaders() {
			const headers = {};
			if (this._authProvider) {
				const tokens = await this._authProvider.tokens();
				if (tokens) headers["Authorization"] = `Bearer ${tokens.access_token}`;
			}
			if (this._protocolVersion) headers["mcp-protocol-version"] = this._protocolVersion;
			const extraHeaders = (0, transport_js_1.normalizeHeaders)(this._requestInit?.headers);
			return new Headers({
				...headers,
				...extraHeaders
			});
		}
		_startOrAuth() {
			const fetchImpl = this?._eventSourceInit?.fetch ?? this._fetch ?? fetch;
			return new Promise((resolve, reject) => {
				this._eventSource = new eventsource_1.EventSource(this._url.href, {
					...this._eventSourceInit,
					fetch: async (url, init) => {
						const headers = await this._commonHeaders();
						headers.set("Accept", "text/event-stream");
						const response = await fetchImpl(url, {
							...init,
							headers
						});
						if (response.status === 401 && response.headers.has("www-authenticate")) {
							const { resourceMetadataUrl, scope } = (0, auth_js_1.extractWWWAuthenticateParams)(response);
							this._resourceMetadataUrl = resourceMetadataUrl;
							this._scope = scope;
						}
						return response;
					}
				});
				this._abortController = new AbortController();
				this._eventSource.onerror = (event) => {
					if (event.code === 401 && this._authProvider) {
						this._authThenStart().then(resolve, reject);
						return;
					}
					const error = new SseError(event.code, event.message, event);
					reject(error);
					this.onerror?.(error);
				};
				this._eventSource.onopen = () => {};
				this._eventSource.addEventListener("endpoint", (event) => {
					const messageEvent = event;
					try {
						this._endpoint = new URL(messageEvent.data, this._url);
						if (this._endpoint.origin !== this._url.origin) throw new Error(`Endpoint origin does not match connection origin: ${this._endpoint.origin}`);
					} catch (error) {
						reject(error);
						this.onerror?.(error);
						this.close();
						return;
					}
					resolve();
				});
				this._eventSource.onmessage = (event) => {
					const messageEvent = event;
					let message;
					try {
						message = types_js_1.JSONRPCMessageSchema.parse(JSON.parse(messageEvent.data));
					} catch (error) {
						this.onerror?.(error);
						return;
					}
					this.onmessage?.(message);
				};
			});
		}
		async start() {
			if (this._eventSource) throw new Error("SSEClientTransport already started! If using Client class, note that connect() calls start() automatically.");
			return await this._startOrAuth();
		}
		/**
		* Call this method after the user has finished authorizing via their user agent and is redirected back to the MCP client application. This will exchange the authorization code for an access token, enabling the next connection attempt to successfully auth.
		*/
		async finishAuth(authorizationCode) {
			if (!this._authProvider) throw new auth_js_1.UnauthorizedError("No auth provider");
			if (await (0, auth_js_1.auth)(this._authProvider, {
				serverUrl: this._url,
				authorizationCode,
				resourceMetadataUrl: this._resourceMetadataUrl,
				scope: this._scope,
				fetchFn: this._fetchWithInit
			}) !== "AUTHORIZED") throw new auth_js_1.UnauthorizedError("Failed to authorize");
		}
		async close() {
			this._abortController?.abort();
			this._eventSource?.close();
			this.onclose?.();
		}
		async send(message) {
			if (!this._endpoint) throw new Error("Not connected");
			try {
				const headers = await this._commonHeaders();
				headers.set("content-type", "application/json");
				const init = {
					...this._requestInit,
					method: "POST",
					headers,
					body: JSON.stringify(message),
					signal: this._abortController?.signal
				};
				const response = await (this._fetch ?? fetch)(this._endpoint, init);
				if (!response.ok) {
					const text = await response.text().catch(() => null);
					if (response.status === 401 && this._authProvider) {
						const { resourceMetadataUrl, scope } = (0, auth_js_1.extractWWWAuthenticateParams)(response);
						this._resourceMetadataUrl = resourceMetadataUrl;
						this._scope = scope;
						if (await (0, auth_js_1.auth)(this._authProvider, {
							serverUrl: this._url,
							resourceMetadataUrl: this._resourceMetadataUrl,
							scope: this._scope,
							fetchFn: this._fetchWithInit
						}) !== "AUTHORIZED") throw new auth_js_1.UnauthorizedError();
						return this.send(message);
					}
					throw new Error(`Error POSTing to endpoint (HTTP ${response.status}): ${text}`);
				}
				await response.body?.cancel();
			} catch (error) {
				this.onerror?.(error);
				throw error;
			}
		}
		setProtocolVersion(version) {
			this._protocolVersion = version;
		}
	};
	exports.SSEClientTransport = SSEClientTransport;
}));
//#endregion
export default require_sse();
export {};

//# sourceMappingURL=sse-DCdDjGeD.js.map