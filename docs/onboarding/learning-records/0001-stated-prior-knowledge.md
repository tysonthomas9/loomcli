# Stated prior knowledge and track choice

The learner is a competent Go engineer who already understands event sourcing,
Redis, and AI-agent orchestration as general concepts, and chose a full-stack
backend-first track over a six-week full ramp. They did **not** claim React or
TypeScript fluency.

## Implications

- Never spend lesson budget on Go idiom, event-sourcing theory, or Redis data
  structures in the abstract. Teach only this system's instantiation of them —
  the actual Lua claim script, the actual key schema, the actual event types.
  Generic explanation here reads as padding and costs working memory that the
  domain specifics need.
- Frontend lessons carry a different contract from backend lessons: show the
  pattern concretely, name the file to copy, and do not lean on React idiom as
  shared vocabulary. A backend lesson can say "the usual table test"; a
  frontend lesson cannot say "the usual hook".
- The learner asked, unprompted, for *navigation* and *implementation patterns*
  alongside architecture — "anything that helps someone navigate the repo, start
  working on new tasks". Treat findability as a first-class skill with its own
  lessons and its own reference card, not as a by-product of architecture
  lessons. This is the strongest steer in the intake and should show up in
  week 1, not week 5.
- Six weeks part-time, 15-40 minute lessons. Any lesson that cannot be completed
  in one sitting must be split.
