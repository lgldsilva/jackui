import { Configuration } from 'electron-builder'

const config: Configuration = {
  appId: 'com.jackui.desktop',
  productName: 'JackUI',
  directories: {
    output: 'dist-electron',
    buildResources: 'electron/tray-icons',
  },
  files: [
    'electron/dist/main.js',
    'electron/dist/preload.js',
  ],
  asar: true,
  extraResources: [
    {
      from: 'dist-electron/',
      to: '.',
      filter: ['jackui-server*'],
    },
    {
      from: 'config.yaml',
      to: 'config.yaml',
    },
    {
      from: 'electron/version.json',
      to: 'version.json',
    },
  ],
  protocols: [
    {
      name: 'JackUI Magnet Link',
      schemes: ['magnet', 'jackui'],
    },
  ],
  mac: {
    category: 'public.app-category.video',
    target: [
      { target: 'dmg', arch: ['arm64'] },
    ],
    icon: 'electron/tray-icons/icon.icns',
    hardenedRuntime: true,
    // Use Electron's default entitlements (needed for V8/GPU/networking).
    // Custom entitlements override Electron's defaults and break startup.
    extendInfo: {
      NSSystemAdministrationUsageDescription: 'JackUI needs network access to communicate with Go server and Jackett.',
    },
  },
  win: {
    target: [
      { target: 'nsis', arch: ['x64'] },
    ],
  },
  linux: {
    target: [
      { target: 'AppImage', arch: ['x64'] },
      { target: 'deb', arch: ['x64'] },
    ],
    category: 'Video',
  },
  nsis: {
    oneClick: false,
    allowToChangeInstallationDirectory: true,
  },
}

export default config
