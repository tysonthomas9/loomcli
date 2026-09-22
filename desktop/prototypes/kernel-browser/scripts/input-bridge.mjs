const pointerTypes = new Set(["mouseMoved", "mousePressed", "mouseReleased", "mouseWheel"]);
const mouseButtons = new Set(["none", "left", "middle", "right", "back", "forward"]);

function finiteNumber(value, label) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw Object.assign(new Error(`${label} must be a finite number`), { status: 400 });
  }
  return value;
}

export function pointerParams(payload, geometry) {
  if (!pointerTypes.has(payload.type)) {
    throw Object.assign(new Error(`unsupported pointer event: ${String(payload.type)}`), { status: 400 });
  }

  const surfaceX = finiteNumber(payload.surfaceX, "pointer surface x");
  const surfaceY = finiteNumber(payload.surfaceY, "pointer surface y");
  const surfaceWidth = finiteNumber(payload.surfaceWidth, "pointer surface width");
  const surfaceHeight = finiteNumber(payload.surfaceHeight, "pointer surface height");
  if (surfaceWidth <= 0 || surfaceHeight <= 0 || surfaceX < 0 || surfaceX > surfaceWidth || surfaceY < 0 || surfaceY > surfaceHeight) {
    throw Object.assign(new Error("pointer surface coordinates must be inside positive surface dimensions"), { status: 400 });
  }

  const screenWidth = finiteNumber(geometry.screen.width, "screen width");
  const screenHeight = finiteNumber(geometry.screen.height, "screen height");
  const outerWidth = finiteNumber(geometry.window.outerWidth, "window outer width");
  const outerHeight = finiteNumber(geometry.window.outerHeight, "window outer height");
  const innerWidth = finiteNumber(geometry.window.innerWidth, "window inner width");
  const innerHeight = finiteNumber(geometry.window.innerHeight, "window inner height");
  const screenX = finiteNumber(geometry.window.screenX, "window screen x");
  const screenY = finiteNumber(geometry.window.screenY, "window screen y");
  if ([screenWidth, screenHeight, outerWidth, outerHeight, innerWidth, innerHeight].some((value) => value <= 0)) {
    throw Object.assign(new Error("browser geometry dimensions must be positive"), { status: 400 });
  }

  const streamScale = Math.min(surfaceWidth / screenWidth, surfaceHeight / screenHeight);
  const streamOffsetX = (surfaceWidth - screenWidth * streamScale) / 2;
  const streamOffsetY = (surfaceHeight - screenHeight * streamScale) / 2;
  const remoteX = (surfaceX - streamOffsetX) / streamScale;
  const remoteY = (surfaceY - streamOffsetY) / streamScale;
  const contentOffsetX = screenX + (outerWidth - innerWidth) / 2;
  const contentOffsetY = screenY + outerHeight - innerHeight;
  let pageX = remoteX - contentOffsetX;
  let pageY = remoteY - contentOffsetY;

  const buttons = payload.buttons ?? 0;
  if (!Number.isInteger(buttons) || buttons < 0 || buttons > 31) {
    throw Object.assign(new Error("mouse buttons must be a bit field from 0 to 31"), { status: 400 });
  }
  const keepDragState = payload.type === "mouseReleased" || buttons !== 0;
  if (remoteX < 0 || remoteX > screenWidth || remoteY < 0 || remoteY > screenHeight || pageX < 0 || pageX > innerWidth || pageY < 0 || pageY > innerHeight) {
    if (!keepDragState) return null;
    pageX = Math.max(0, Math.min(innerWidth, pageX));
    pageY = Math.max(0, Math.min(innerHeight, pageY));
  }

  const button = payload.button ?? "none";
  if (!mouseButtons.has(button)) {
    throw Object.assign(new Error(`unsupported mouse button: ${String(button)}`), { status: 400 });
  }

  const params = {
    type: payload.type,
    x: Math.round(pageX),
    y: Math.round(pageY),
    button,
  };

  if (buttons !== 0) params.buttons = buttons;
  const modifiers = payload.modifiers ?? 0;
  if (!Number.isInteger(modifiers) || modifiers < 0 || modifiers > 15) {
    throw Object.assign(new Error("pointer modifiers must be a bit field from 0 to 15"), { status: 400 });
  }
  if (modifiers !== 0) params.modifiers = modifiers;

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
