import assert from 'node:assert/strict';
import { test } from 'node:test';
import { redact } from './protocol.js';
import { redactionFacts } from './redaction.js';

test('omitted credential fields differ from source absence and sanitized text paths retain no secrets', () => {
  const raw = { authorization:'Bearer actual-private-token', nested:{access_token:'actual-private-token',text:'prefix actual-private-token suffix'},safe:'visible' };
  const facts = redactionFacts(raw,['actual-private-token']);
  assert.deepEqual(facts.omittedPaths,['/authorization','/nested/access_token']);
  assert.deepEqual(facts.replacedTextPaths,['/nested/text']);
  assert.deepEqual(redact(raw,['actual-private-token']),{nested:{text:'prefix [REDACTED] suffix'},safe:'visible'});
  assert.deepEqual(redactionFacts({nested:{text:'plain'},safe:'visible'}),{omittedPaths:[],replacedTextPaths:[]});
  assert.ok(!JSON.stringify(facts).includes('actual-private-token'));
});
test('already redacted input remains distinct from adapter replacement and private keys fail closed', () => {
  assert.deepEqual(redactionFacts({command:'[REDACTED]'}),{omittedPaths:[],replacedTextPaths:[]});
  const raw = {'sk-abcdefgh12345678':'value'};
  assert.throws(()=>redactionFacts(raw),/private material/);
  assert.throws(()=>redact(raw),/private material/);
});
