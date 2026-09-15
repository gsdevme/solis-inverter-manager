package inverter

import "testing"

func TestStatusLabel(t *testing.T) {
	cases := []struct {
		name string
		code uint16
		want string
	}{
		{name: "generating (the live value on this unit)", code: 3, want: "Generating"},
		{name: "waiting", code: 0x0000, want: "Waiting"},
		{name: "normal", code: 0x000F, want: "Normal"},
		{name: "battery comms failure", code: 0x2012, want: "CAN_Comm_FAIL"},
		{name: "bms alarm", code: 0x2015, want: "Alarm-BMS"},
		{name: "bms alarm 2", code: 0x2017, want: "Alarm2-BMS"},
		{name: "battery not connected", code: 0x1055, want: "NO-Battery"},
		{name: "fan alarm", code: 0xF011, want: "Fan Alarm"},
		{name: "unknown falls back to hex", code: 0xBEEF, want: "0xBEEF"},
		{name: "unknown gap in a known range", code: 0x2013, want: "0x2013"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StatusLabel(tc.code); got != tc.want {
				t.Errorf("StatusLabel(0x%04X) = %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}

// TestStatusLabelsAreUnique guards the table against a copy-paste duplicate: two
// codes sharing one label would make the status_text sensor ambiguous in history.
func TestStatusLabelsAreUnique(t *testing.T) {
	seen := map[string]uint16{}
	for code, label := range statusLabels {
		if prev, dup := seen[label]; dup {
			t.Errorf("label %q used by both 0x%04X and 0x%04X", label, prev, code)
		}
		seen[label] = code
	}
}
