//go:build !rawhttp_poison

package rawhttp

// PoisonBuildEnabled reports whether this binary was built with -tags rawhttp_poison.
func PoisonBuildEnabled() bool { return false }

func poisonPinnedBuffer(cr *connReader) {}

func poisonCtxRequestSlices(ctx *Ctx) {}
