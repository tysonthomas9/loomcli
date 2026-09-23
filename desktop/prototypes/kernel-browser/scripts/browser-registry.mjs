const maxBrowsers = 8;

function browserIndex(id) {
  const match = /^app-([a-z])$/.exec(id);
  return match ? match[1].charCodeAt(0) - 96 : 0;
}

export function nextBrowserDefinition(existingBrowsers) {
  const highestIndex = existingBrowsers.reduce((highest, browser) => Math.max(highest, browserIndex(browser.id)), 0);
  const index = highestIndex + 1;
  if (index > maxBrowsers) throw new Error(`prototype supports at most ${maxBrowsers} browser apps`);

  const portPrefix = 60 + index;
  return {
    id: `app-${String.fromCharCode(96 + index)}`,
    name: `Browser ${index}`,
    livePort: portPrefix * 1000 + 80,
    cdpPort: portPrefix * 1000 + 222,
    webdriverPort: portPrefix * 1000 + 224,
    apiPort: portPrefix * 1000 + 101,
    mediaPort: 55900 + index * 100,
    epoch: 0,
  };
}
