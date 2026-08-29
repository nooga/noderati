package host

const properLockfileShim = `function release() {}
export function lockSync(_path, _opts) { return release; }
export async function lock(_path, _opts) { return release; }
export default { lockSync, lock };
`

func declareProperLockfile() {
	registerJSShim("proper-lockfile", properLockfileShim)
}
