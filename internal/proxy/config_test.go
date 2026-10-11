package proxy

import "testing"

func TestCustomFieldPolicy(t *testing.T) {
	c := Config{Rules: []Rule{{Origin: "https://example.test", Path: "/echo", Adapter: "demo"}}, Detectors: []string{"guid", "email", "fields"}, SensitiveFields: []string{"employeeId", "deviceId"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	p, err := FieldPolicy(c)
	if err != nil || !p.GUID || !p.Email || !p.Fields || p.VIN || len(p.CustomFields) != 2 {
		t.Fatal("configuration did not reach masking policy")
	}
	c.SensitiveFields = []string{"field.*"}
	if c.Validate() == nil {
		t.Fatal("invalid field name accepted")
	}
}
