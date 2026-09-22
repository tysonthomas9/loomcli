export async function captureFrame(send) {
  const screenshot = await send("Page.captureScreenshot", {
    format: "jpeg",
    quality: 70,
    fromSurface: true,
  });
  return { mimeType: "image/jpeg", image: screenshot.data };
}
