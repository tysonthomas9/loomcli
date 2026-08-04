import { access, readFile } from "node:fs/promises";

const requiredFiles = [
  "index.html",
  "src/app.js",
  "src/data.js",
  "src/styles.css",
];

await Promise.all(requiredFiles.map((file) => access(file)));

const html = await readFile("index.html", "utf8");
for (const reference of ["./src/styles.css", "./src/app.js"]) {
  if (!html.includes(reference)) {
    throw new Error(`index.html is missing ${reference}`);
  }
}

console.log("Mock Slack app shell is buildable.");
