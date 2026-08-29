package host

const hostedGitInfoShim = `export function fromUrl(_url) {
  return { type: "github", user: "", project: "", committish: null };
}
export default { fromUrl };
`

func declareHostedGitInfo() {
	registerJSShim("hosted-git-info", hostedGitInfoShim)
}
