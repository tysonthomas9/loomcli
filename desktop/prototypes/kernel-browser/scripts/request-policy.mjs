export const allowedOrigins = new Set([
  "http://127.0.0.1:1421",
  "http://tauri.localhost",
  "tauri://localhost",
]);

export function originAllowed(origin) {
  return !origin || allowedOrigins.has(origin);
}
