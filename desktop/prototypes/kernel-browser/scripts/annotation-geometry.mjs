export function annotationGeometry(rect, viewport) {
  const bounds = { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
  return {
    bounds,
    documentBounds: {
      x: rect.x + viewport.pageLeft,
      y: rect.y + viewport.pageTop,
      width: rect.width,
      height: rect.height,
    },
  };
}
