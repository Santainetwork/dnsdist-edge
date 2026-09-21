package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeWhitelist(t *testing.T) {
	longLabel := strings.Repeat("a", 64) + ".example"
	validLongLabel := strings.Repeat("a", 63) + ".example"
	longDomain := strings.Repeat("a.", 126) + "a"
	tooLongDomain := longDomain + "a"
	tests := []struct {
		name              string
		input, want       string
		invalid           []int
		count, duplicates int
	}{
		{
			name:       "canonical domain ip comment dedupe stable order",
			input:      " Example.COM.\n192.0.2.1\n# note\nexample.com\n# note\n192.0.2.1\n",
			want:       "example.com\n192.0.2.1\n# note\n# note\n",
			count:      2,
			duplicates: 2,
		},
		{
			name:  "ipv6",
			input: "2001:DB8::1\n",
			want:  "2001:db8::1\n",
			count: 1,
		},
		{
			name:    "invalid url wildcard whitespace hosts adguard",
			input:   "https://example.com\n*.example.com\nfoo bar\nexample.com/path\n0.0.0.0 example.com\n||example.com^\n",
			invalid: []int{1, 2, 3, 4, 5, 6},
		},
		{
			name:    "invalid label",
			input:   "-bad.example\nvalid.example\nbad-.example\n",
			want:    "valid.example\n",
			invalid: []int{1, 3},
			count:   1,
		},
		{
			name:  "punycode accepted",
			input: "XN--BCHER-KVA.example.\n",
			want:  "xn--bcher-kva.example\n",
			count: 1,
		},
		{
			name:    "unicode rejected",
			input:   "münchen.example\n例え.テスト\n",
			invalid: []int{1, 2},
		},
		{
			name:    "label and domain limits",
			input:   longLabel + "\n" + validLongLabel + "\n" + longDomain + "\n" + tooLongDomain + "\n",
			want:    validLongLabel + "\n" + longDomain + "\n",
			invalid: []int{1, 4},
			count:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, invalid, count, duplicates := normalizeWhitelist(tt.input)
			if got != tt.want {
				t.Fatalf("normalized = %q, want %q", got, tt.want)
			}
			if !reflect.DeepEqual(invalid, tt.invalid) {
				t.Fatalf("invalid = %v, want %v", invalid, tt.invalid)
			}
			if count != tt.count || duplicates != tt.duplicates {
				t.Fatalf("count/duplicates = %d/%d, want %d/%d", count, duplicates, tt.count, tt.duplicates)
			}
		})
	}
}
