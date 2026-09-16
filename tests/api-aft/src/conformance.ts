// L4: spec conformance. ADVISORY -- it never fails a run on its own.
//
// Why advisory: both specs are demonstrably weak oracles (see `doctor` for the
// census). A conformance miss here is at least as likely to be a stale spec as a
// broken handler, so the finding carries a verdict, not a verdict-shaped assertion.
//
// Two passes per response:
//   lax    -- the schema as written. A failure means a promise was broken.
//   strict -- the unevaluatedProperties:false twin. A failure means the response
//             carries fields the spec never documented. Invisible without the twin,
//             because neither spec closes a single object.

import { Ajv2020 } from "ajv/dist/2020.js";
import addFormats from "ajv-formats";
import { matchOperation, power, strictify, type ContractIndex } from "./contract.ts";
import type { Exchange } from "./wire.ts";

export type ConformanceIssue = {
  kind: "UNDECLARED_STATUS" | "SCHEMA_VIOLATION" | "UNDOCUMENTED_FIELD" | "NO_SCHEMA" | "UNROUTED";
  service: string;
  operationId: string;
  method: string;
  pathname: string;
  status: number;
  detail: string;
  power: number;
};

const ajv = new Ajv2020({ strict: false, allErrors: true, validateFormats: false });
addFormats.default ? addFormats.default(ajv) : (addFormats as unknown as (a: unknown) => void)(ajv);

function validate(schema: unknown, data: unknown): string[] {
  if (schema === undefined || schema === null || schema === true) return [];
  try {
    const fn = ajv.compile(schema as object);
    if (fn(data)) return [];
    return (fn.errors ?? []).map((e) => `${e.instancePath || "$"} ${e.message ?? ""}`.trim());
  } catch (err) {
    return [`SCHEMA_COMPILE_ERROR: ${(err as Error).message}`];
  }
}

export function checkExchange(index: ContractIndex, ex: Exchange): ConformanceIssue[] {
  const out: ConformanceIssue[] = [];
  const m = matchOperation(index, ex.method, ex.pathname);
  if (!m) {
    out.push({
      kind: "UNROUTED",
      service: ex.service,
      operationId: "-",
      method: ex.method,
      pathname: ex.pathname,
      status: ex.status,
      detail: "observed a live route with no operation in the spec",
      power: 0,
    });
    return out;
  }
  const { op } = m;
  const base = {
    service: ex.service,
    operationId: op.operationId,
    method: ex.method,
    pathname: ex.pathname,
    status: ex.status,
  };

  const code = String(ex.status);
  if (op.declaredStatuses.length > 0 && !op.declaredStatuses.includes(code) && !op.declaredStatuses.includes("default")) {
    out.push({ ...base, kind: "UNDECLARED_STATUS", detail: `spec declares [${op.declaredStatuses.join(", ")}]`, power: 1 });
  }

  const schema = op.responses.get(code);
  if (schema === undefined) {
    if (ex.status >= 200 && ex.status < 300) {
      out.push({ ...base, kind: "NO_SCHEMA", detail: `no application/json schema declared for ${code}`, power: 0 });
    }
    return out;
  }

  const p = power(schema);
  const laxErrors = validate(schema, ex.body);
  for (const e of laxErrors) {
    out.push({ ...base, kind: "SCHEMA_VIOLATION", detail: e, power: p });
  }
  if (laxErrors.length === 0) {
    const strictErrors = validate(strictify(schema), ex.body);
    for (const e of strictErrors) {
      out.push({ ...base, kind: "UNDOCUMENTED_FIELD", detail: e, power: p });
    }
  }
  return out;
}
