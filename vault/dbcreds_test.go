package vault

import (
	"encoding/json"
	"testing"
)

type fakeDbClient struct {
	SecretsClient
	data map[string]*SecretResponse
}

func (f *fakeDbClient) Read(path string) (*SecretResponse, error) {
	return f.data[path], nil
}

func TestGetDbCredentialsHostsPort(t *testing.T) {
	tests := []struct {
		name  string
		hosts string
		port  interface{}
		want  string
	}{
		{"string port", "cassandra", "9042", "cassandra:9042"},
		{"json.Number port", "cassandra", json.Number("9042"), "cassandra:9042"},
		{"float64 port", "cassandra", float64(9142), "cassandra:9142"},
		{"multiple hosts", "a,b", "9042", "a:9042,b:9042"},
		{"no port", "cassandra", nil, "cassandra"},
		{"invalid port type", "cassandra", true, "cassandra"},
		{"padded string port", "cassandra", " 9042 ", "cassandra:9042"},
		{"int port", "cassandra", 9042, "cassandra:9042"},
		{"int64 port", "cassandra", int64(9042), "cassandra:9042"},
		{"non-numeric port", "cassandra", "abc", "cassandra"},
		{"zero port", "cassandra", "0", "cassandra"},
		{"negative port", "cassandra", "-1", "cassandra"},
		{"port too large", "cassandra", "70000", "cassandra"},
		{"fractional float port", "cassandra", 90.5, "cassandra"},
		{"empty string port", "cassandra", "", "cassandra"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := map[string]interface{}{"hosts": tt.hosts}
			if tt.port != nil {
				conn["port"] = tt.port
			}
			old := client
			t.Cleanup(func() { client = old })
			client = &fakeDbClient{data: map[string]*SecretResponse{
				"cassandra/creds/role": {
					LeaseID: "l",
					Data:    map[string]interface{}{"username": "u", "password": "p"},
				},
				"cassandra/config/cassandra": {
					Data: map[string]interface{}{"connection_details": conn},
				},
			}}
			got, err := GetDbCredentials("role", "cassandra")
			if err != nil {
				t.Fatal(err)
			}
			if got.Hosts != tt.want {
				t.Errorf("Hosts = %q, want %q", got.Hosts, tt.want)
			}
		})
	}
}
