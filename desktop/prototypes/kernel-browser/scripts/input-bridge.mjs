const pointerTypes = new Set(["mouseMoved", "mousePressed", "mouseReleased", "mouseWheel"]);
const mouseButtons = new Set(["none", "left", "middle", "right", "back", "forward"]);

function finiteNumber(value, label) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw Object.assign(new Error(`${label} must be a finite number`), { status: 400 });
  }
  return value;
}

export function pointerParams(payload, viewport) {
  if (!pointerTypes.has(payload.type)) {
    throw Object.assign(new Error(`unsupported pointer event: ${String(payload.type)}`), { status: 400 });
  }

  const x = finiteNumber(payload.x, "pointer x");
  const y = finiteNumber(payload.y, "pointer y");
  if (x < 0 || x > 1 || y < 0 || y > 1) {
    throw Object.assign(new Error("normalized pointer coordinates must be between 0 and 1"), { status: 400 });
  }

  const width = finiteNumber(viewport.width, "viewport width");
  const height = finiteNumber(viewport.height, "viewport height");
  if (width <= 0 || height <= 0) {
    throw Object.assign(new Error("viewport dimensions must be positive"), { status: 400 });
  }

  const button = payload.button ?? "none";
  if (!mouseButtons.has(button)) {
    throw Object.assign(new Error(`unsupported mouse button: ${String(button)}`), { status: 400 });
  }

  const params = {
    type: payload.type,
    x: Math.round(x * width),
    y: Math.round(y * height),
    button,
  };

  if (payload.clickCount !== undefined) {
    const clickCount = finiteNumber(payload.clickCount, "click count");
    if (!Number.isInteger(clickCount) || clickCount < 0) {
      throw Object.assign(new Error("click count must be a non-negative integer"), { status: 400 });
    }
    params.clickCount = clickCount;
  }

  if (payload.type === "mouseWheel") {
    params.deltaX = finiteNumber(payload.deltaX ?? 0, "wheel delta x");
    params.deltaY = finiteNumber(payload.deltaY ?? 0, "wheel delta y");
  }

  return params;
}

export function keyParams(payload) {
  if (payload.type !== "keyDown" && payload.type !== "keyUp") {
    throw Object.assign(new Error(`unsupported key event: ${String(payload.type)}`), { status: 400 });
  }
  if (typeof payload.key !== "string" || payload.key.length === 0 || payload.key.length > 64) {
    throw Object.assign(new Error("keyboard key must be a non-empty string"), { status: 400 });
  }
  if (typeof payload.code !== "string" || payload.code.length === 0 || payload.code.length > 64) {
    throw Object.assign(new Error("keyboard code must be a non-empty string"), { status: 400 });
  }

  const modifiers = payload.modifiers ?? 0;
  if (!Number.isInteger(modifiers) || modifiers < 0 || modifiers > 15) {
    throw Object.assign(new Error("keyboard modifiers must be a bit field from 0 to 15"), { status: 400 });
  }

  const params = { type: payload.type, key: payload.key, code: payload.code };
  if (payload.text !== undefined) {
    if (typeof payload.text !== "string" || payload.text.length > 16) {
      throw Object.assign(new Error("keyboard text must be a short string"), { status: 400 });
    }
    params.text = payload.text;
  }
  if (modifiers !== 0) params.modifiers = modifiers;
  return params;
}
