import { z } from 'zod';
import { redact, requireFact, privateEvidenceField, type Json } from './protocol.js';

export const RedactionFacts = z.object({
  omittedPaths: z.array(z.string().max(4096)).max(100000),
  replacedTextPaths: z.array(z.string().max(4096)).max(100000),
}).strict();
/** Safe structural receipts distinguish sanitization from source absence.
 * No credential values, hashes or generic credential-match counts are retained. */
export function redactionFacts(value: Json, secrets: readonly string[] = []): z.infer<typeof RedactionFacts> {
  const result: z.infer<typeof RedactionFacts> = { omittedPaths: [], replacedTextPaths: [] };
  const visit = (data: Json, pointer: string, depth: number) => {
    requireFact(depth <= 64, 'observation-failed', 'Observation redaction nesting exceeds bound');
    if (typeof data === 'string') { if (redact(data, secrets) !== data) result.replacedTextPaths.push(pointer); }
    else if (Array.isArray(data)) data.forEach((child, index) => visit(child, `${pointer}/${index}`, depth + 1));
    else if (data && typeof data === 'object') for (const [key, child] of Object.entries(data)) {
      requireFact(redact(key, secrets) === key, 'observation-failed', 'Observation key contains private material');
      const path = `${pointer}/${key.replace(/~/g, '~0').replace(/\//g, '~1')}`;
      if (privateEvidenceField.test(key)) result.omittedPaths.push(path);
      else visit(child, path, depth + 1);
    }
  };
  visit(value, '', 0); return RedactionFacts.parse(result);
}
