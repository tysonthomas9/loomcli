package feedback

import "testing"

func TestAddressCommandUsesChangeAndDelivery(t *testing.T) {
	if addressCmd.Use != "address <change>" {
		t.Fatalf("address usage = %q", addressCmd.Use)
	}
	if flag := addressCmd.Flags().Lookup("delivery-id"); flag == nil {
		t.Fatal("address command does not select a verified delivery")
	}
}
