import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';

interface SourceRecord { id: string; path: string; file_sha256: string; line: number; end_line: number; source: string }
interface OriginalCase { id: string; source: { file_id: string; line: number; end_line: number }; file_sha256: string; exact_original_case_source: string; coverage_removed: boolean }
const root = new URL('../../../../', import.meta.url);
const data = JSON.parse(readFileSync(new URL('./original-contracts.json', import.meta.url), 'utf8')) as {
  source_records: SourceRecord[]; predicates: { id: string; source_ref: string; line: number; expression: string }[]; cases: OriginalCase[];
};
const lines = (value: string) => value.match(/[^\n]*\n|[^\n]+$/g) ?? [];
test('static preservation map retains exact original source and case predicates', () => {
  const ids = new Set<string>();
  for (const record of data.source_records) {
    assert.ok(!ids.has(record.id)); ids.add(record.id);
    const raw = readFileSync(new URL(record.path, root));
    assert.equal(createHash('sha256').update(raw).digest('hex'), record.file_sha256);
    assert.equal(lines(raw.toString()).slice(record.line - 1, record.end_line).join(''), record.source);
  }
  for (const predicate of data.predicates) {
    const source = data.source_records.find(record => record.id === predicate.source_ref)!;
    assert.ok(source && predicate.line >= source.line && predicate.line <= source.end_line);
    assert.ok(predicate.expression.length > 0);
  }
  for (const original of data.cases) {
    const path = original.source.file_id.replace(/^campaign:/, '');
    const raw = readFileSync(new URL(path, root));
    assert.equal(createHash('sha256').update(raw).digest('hex'), original.file_sha256);
    assert.equal(lines(raw.toString()).slice(original.source.line - 1, original.source.end_line).join(''), original.exact_original_case_source);
    assert.equal(original.coverage_removed, false);
  }
  assert.equal(data.source_records.length, 24);
  assert.equal(data.cases.length, 10);
  assert.ok(data.predicates.some(row => row.source_ref.endsWith('#replay_u3') && row.id.includes('/demand/')));
});
