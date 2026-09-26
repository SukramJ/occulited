package system

import (
	"context"
	"os"

	"github.com/hobbyquaker/occulited/internal/trust"
)

// TrustWriter is the trust stores' way to the system store's files (openccu-lite task 231): the
// administrator's CA files under /usr/local/share/ca-certificates, the distrust file and the
// bundle's rebuild, all root's, through the privilege helper (Priv).
type TrustWriter struct{}

var _ trust.SystemWriter = TrustWriter{}

func (TrustWriter) WriteFile(path string, data []byte, mode os.FileMode) error {
	return Priv.WriteFile(path, data, mode)
}
func (TrustWriter) Remove(path string) error                     { return Priv.Remove(path) }
func (TrustWriter) MkdirAll(path string, mode os.FileMode) error { return Priv.MkdirAll(path, mode) }
func (TrustWriter) Run(ctx context.Context, name string, args []string) (trust.RunResult, error) {
	res, err := Priv.Run(ctx, name, args, nil)
	if err != nil {
		return trust.RunResult{}, err
	}
	return trust.RunResult{Exit: res.Exit, Output: string(res.Stdout) + string(res.Stderr)}, nil
}
