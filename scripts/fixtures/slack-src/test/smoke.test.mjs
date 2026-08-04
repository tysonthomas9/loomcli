import assert from "node:assert/strict";
import test from "node:test";

import { channels, messages, teams, users } from "../src/data.js";

test("seed data has enough structure for Slack workflows", () => {
  assert.equal(teams.length, 2);
  assert.ok(users.length >= 3);
  assert.ok(channels.some((channel) => channel.teamId === teams[0].id));
  assert.ok(messages.every((message) => channels.some((c) => c.id === message.channelId)));
});
