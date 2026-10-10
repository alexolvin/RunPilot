package node

import (
	"reflect"
	"testing"
)

// TestCoderArgsFromEnv — W14: --approval-mode yolo всегда; auth/model/baseUrl
// — только если в env есть шлюз.
func TestCoderArgsFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{
			name: "no-gateway",
			env:  map[string]string{},
			want: []string{"--approval-mode", "yolo"},
		},
		{
			name: "with-gateway",
			env:  map[string]string{"OPENAI_MODEL": "alias", "OPENAI_BASE_URL": "http://gw/s/SS1/v1"},
			want: []string{
				"--approval-mode", "yolo",
				"--auth-type", "openai",
				"--model", "alias",
				"--openai-base-url", "http://gw/s/SS1/v1",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CoderArgsFromEnv(c.env); !reflect.DeepEqual(got, c.want) {
				t.Errorf("CoderArgsFromEnv = %v, хочу %v", got, c.want)
			}
		})
	}
}
