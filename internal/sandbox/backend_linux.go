//go:build linux

package sandbox

import "context"

func platformLaunch(ctx context.Context, req *LaunchRequest) ([]byte, error) {
	return landlockLaunch(ctx, req)
}
