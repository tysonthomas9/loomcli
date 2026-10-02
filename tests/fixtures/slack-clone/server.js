import { createServer } from 'node:http';
import { createHandler, createStore } from './app.js';

const port = Number(process.env.PORT || 3456);
const store = createStore(process.env.DATA_FILE || 'data.json');
createServer(createHandler(store)).listen(port, () => console.log(`slack-clone on http://localhost:${port}`));
