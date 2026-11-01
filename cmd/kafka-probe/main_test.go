package main

import (
	"bytes"
	"context"
	"testing"
)

func TestInvalidProbeArgumentsFailBeforeLoadingCredentials(t *testing.T) {
	for _, args := range [][]string{
		nil, {"-key", "wrong-prefix"}, {"-mode", "invalid", "-key", "smoke-test"},
		{"-mode", "read", "-key", "smoke-test", "-partition", "1", "-offset", "0"},
		{"-mode", "read", "-key", "smoke-test", "-partition", "0", "-offset", "-1"},
	} {
		var out bytes.Buffer
		if err := run(context.Background(), args, &out); err == nil || out.Len() != 0 {
			t.Fatalf("invalid args accepted: %v, err=%v", args, err)
		}
	}
}
