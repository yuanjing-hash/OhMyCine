# OhMyCine App Icon

`icon.png` is the transparent master artwork for the OhMyCine "cinema eye" mark.

Generated bundle assets:

- `32x32.png`
- `128x128.png`
- `128x128@2x.png`
- `icon.ico`
- `icon.icns`

Windows NSIS installer and uninstaller icons are explicitly configured in `tauri.windows.conf.json` so release installers do not fall back to the generic NSIS icon.

Regenerate derived icons from the repository root:

```bash
npx tauri icon src-tauri/icons/icon.png --output src-tauri/icons
```

The command also creates platform assets that are not currently tracked. Keep only the master and the bundle files used by `tauri.conf.json` until the Android or iOS project is enabled.
