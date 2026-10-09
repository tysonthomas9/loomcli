import { createHash } from 'node:crypto';
import { boundedText, requireProjection } from './text.js';

export const receiptReplayContract = 'loom-agent-message-receipt-replay' as const;
export const receiptSourceVersions = {
  'receipts-stream': '065178d9c5b4c77f80e3fb1ef7cd78eb63a62ff3c8a452617daeda8992a59dcd',
  'children-u3': '6a032b2bb8f6f35887b21e68d0cf10457f9f12a98ee06bbc2b3ab55d7473196a',
} as const;
export type ReceiptSource = keyof typeof receiptSourceVersions;
export interface SendResult {
  message_id: string; state: 'waiting' | 'handed'; replaced: boolean;
  interrupted?: boolean; turn_id?: string;
}
export interface ReceiptRecord { requestId: string; agentId: string; result: SendResult }
export interface HandoverEvidence {
  complete: boolean;
  delivered: { event_id: string; text: string; inputKey: string; agent_id?: string }[];
  delivery: { event_id: string; text: string; input_key: string }[];
  native: { agent_id: string; input_key: string; native_user_message_count: number }[];
}
export interface ReceiptComparisonInput {
  source: ReceiptSource; sourceSha256: string; complete: boolean;
  original: ReceiptRecord; retry: ReceiptRecord; text: string;
  handover?: HandoverEvidence;
}

function validResult(value: SendResult, source: ReceiptSource): boolean {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const keys = Object.keys(value);
  return ['message_id', 'state', 'replaced'].every(key => Object.hasOwn(value, key)) &&
    (source !== 'children-u3' || Object.hasOwn(value, 'interrupted')) &&
    keys.every(key => ['message_id', 'state', 'replaced', 'interrupted', 'turn_id'].includes(key)) &&
    typeof value.message_id === 'string' && value.message_id.length > 0 && value.message_id.length <= 512 &&
    ['waiting', 'handed'].includes(value.state) && typeof value.replaced === 'boolean' &&
    (!keys.includes('interrupted') || typeof value.interrupted === 'boolean') &&
    (source !== 'children-u3' || typeof value.interrupted === 'boolean') &&
    (!keys.includes('turn_id') || (typeof value.turn_id === 'string' && value.turn_id.length <= 512));
}

function hasHandover(input: ReceiptComparisonInput): boolean {
  const proof = input.handover;
  if (!proof || proof.complete !== true || !Array.isArray(proof.delivered) || !Array.isArray(proof.delivery) || !Array.isArray(proof.native) ||
    proof.delivered.length > 20000 || proof.delivery.length > 2 || proof.native.length > 2) return false;
  const key = 'msg_' + createHash('sha256').update(input.original.agentId + '\0' + input.original.requestId).digest('hex').slice(0, 26);
  // These source variants have different proof obligations. Do not normalize
  // the U3 pair/order requirement into the receipts-stream singleton contract.
  const rows = proof.delivered.filter(row => row && row.text === input.text && row.inputKey === key &&
    (input.source !== 'children-u3' || row.agent_id === input.original.agentId));
  if (rows.length !== 1 || !rows[0]?.event_id) return false;
  const count = input.source === 'children-u3' ? 2 : 1;
  if (proof.delivery.length !== count || proof.native.length !== count) return false;
  const delivery = proof.delivery[0], native = proof.native[0];
  return delivery?.event_id === rows[0].event_id && delivery.input_key === key &&
    (input.source === 'children-u3' || delivery.text === input.text) &&
    native?.agent_id === input.original.agentId && native.input_key === key && native.native_user_message_count === 1;
}

// Supplemental comparison only. Event/slot/native immutability after a real
// replay remains a separate original E2E contract, recorded in coverage.json.
export function compareReceiptFields(input: ReceiptComparisonInput) {
  requireProjection(input?.complete === true, 'incomplete-observation', 'Receipt observation is incomplete');
  requireProjection(Object.hasOwn(receiptSourceVersions, input.source) && input.sourceSha256 === receiptSourceVersions[input.source],
    'unsupported-input', 'Receipt source version does not match');
  boundedText(input.text);
  requireProjection(input.original && input.retry, 'invalid-input', 'Receipt records are missing');
  for (const record of [input.original, input.retry]) {
    boundedText(record.requestId, 512);
    boundedText(record.agentId, 512);
    requireProjection(record.requestId.length > 0 && /^agt_[A-Za-z0-9_-]+$/.test(record.agentId), 'invalid-input', 'Missing receipt identity');
    requireProjection(validResult(record.result, input.source), 'invalid-input', 'Invalid public send receipt');
  }
  requireProjection(input.original.requestId === input.retry.requestId && input.original.agentId === input.retry.agentId,
    'invalid-input', 'Receipt replay identity changed');
  const a = input.original.result, b = input.retry.result;
  const keys = Object.keys(a) as (keyof SendResult)[];
  requireProjection(keys.length === Object.keys(b).length && keys.every(key => Object.hasOwn(b, key)),
    'invalid-input', 'Receipt fields changed');
  requireProjection(keys.filter(key => key !== 'state').every(key => a[key] === b[key]), 'invalid-input', 'Stable receipt values changed');
  const progressed = a.state !== b.state;
  requireProjection(!progressed || (a.state === 'waiting' && b.state === 'handed' && hasHandover(input)),
    'invalid-input', 'Receipt progression lacks source-specific handover evidence');
  return { contract: receiptReplayContract, source: input.source, progressed, receipt: { ...b } };
}
