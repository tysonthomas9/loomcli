# Backlog

Small, independent tickets. Each one should come with a unit test, and `npm test` must stay green.

## 1. Edit a message
Add `PATCH /api/channels/:channel/messages/:id` with `{ "text" }`. It updates the text and sets `editedAt`. The page shows "(edited)" next to edited messages.

## 2. Delete a message
Add `DELETE /api/channels/:channel/messages/:id`. The message disappears from the channel listing, and an unknown id returns 404.

## 3. Create a channel
Add `POST /api/channels` with `{ "name" }`. Names are lowercase letters, digits and dashes, and duplicates return 400. The page gets a "new channel" input.

## 4. Channel topic
Channels get an optional topic. `PUT /api/channels/:channel/topic` with `{ "topic" }` sets it, and the page shows it under the channel heading.

## 5. Search messages
Add `GET /api/search?q=` returning messages from every channel whose text contains `q`, case-insensitive.

## 6. Unread counts
`GET /api/channels?user=` returns each channel with the number of messages posted since that user last read it. Reading a channel's messages with `?user=` marks it read.
