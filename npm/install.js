const { execSync } = require('child_process');
const fs = require('fs');
const path = require('path');
const https = require('https');

const VERSION = '0.1.0';
const REPO = 'tysonthomas9/loomcli';

function getPlatform() {
  const platform = process.platform;
  const arch = process.arch;

  const platformMap = {
    'darwin-x64': 'darwin_amd64',
    'darwin-arm64': 'darwin_arm64',
    'linux-x64': 'linux_amd64',
    'linux-arm64': 'linux_arm64',
    'win32-x64': 'windows_amd64',
  };

  const key = `${platform}-${arch}`;
  return platformMap[key];
}

function download(url, dest) {
  return new Promise((resolve, reject) => {
    const file = fs.createWriteStream(dest);
    https.get(url, (response) => {
      if (response.statusCode === 302 || response.statusCode === 301) {
        // Follow redirect
        file.close();
        fs.unlinkSync(dest);
        download(response.headers.location, dest).then(resolve).catch(reject);
        return;
      }
      if (response.statusCode !== 200) {
        file.close();
        fs.unlinkSync(dest);
        reject(new Error(`Failed to download: ${response.statusCode}`));
        return;
      }
      response.pipe(file);
      file.on('finish', () => {
        file.close(resolve);
      });
    }).on('error', (err) => {
      file.close();
      fs.unlinkSync(dest);
      reject(err);
    });
  });
}

async function install() {
  const platform = getPlatform();
  if (!platform) {
    console.error(`Unsupported platform: ${process.platform}-${process.arch}`);
    process.exit(1);
  }

  const ext = process.platform === 'win32' ? 'zip' : 'tar.gz';
  const filename = `loomcli_${VERSION}_${platform}.${ext}`;
  const url = `https://github.com/${REPO}/releases/download/v${VERSION}/${filename}`;

  const binDir = path.join(__dirname, 'bin');
  if (!fs.existsSync(binDir)) {
    fs.mkdirSync(binDir, { recursive: true });
  }

  const archivePath = path.join(binDir, filename);
  const binaryName = process.platform === 'win32' ? 'loom.exe' : 'loom';
  const binaryPath = path.join(binDir, binaryName);

  // Skip if already installed
  if (fs.existsSync(binaryPath)) {
    const stat = fs.statSync(binaryPath);
    if (stat.size > 1000) {
      console.log('loom already installed');
      return;
    }
  }

  console.log(`Downloading loom v${VERSION} for ${platform}...`);
  await download(url, archivePath);

  // Extract
  if (ext === 'tar.gz') {
    execSync(`tar -xzf "${filename}"`, { cwd: binDir });
    fs.unlinkSync(archivePath);
    fs.chmodSync(binaryPath, 0o755);
  } else {
    execSync(`unzip -o "${filename}"`, { cwd: binDir });
    fs.unlinkSync(archivePath);
  }

  console.log('loom installed successfully!');
}

install().catch((err) => {
  console.error('Failed to install loom:', err.message);
  process.exit(1);
});
