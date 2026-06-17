// Internal helpers shared by driver.js and runner.js. Not part of the public API.

export function trim(value) {
  if (value === undefined || value === null) {
    return "";
  }
  return String(value).trim();
}

export function pickEnv(env, ...names) {
  for (const name of names) {
    const value = trim(env?.[name]);
    if (value) {
      return value;
    }
  }
  return "";
}
