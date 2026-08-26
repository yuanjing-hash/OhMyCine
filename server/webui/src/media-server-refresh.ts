export function canTestMediaServerRefreshTarget(canTestConnection: boolean, canReadMediaLibraries: boolean) {
  return canTestConnection && canReadMediaLibraries
}
