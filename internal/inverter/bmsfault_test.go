package inverter

import "testing"

func TestDecodeBMSFault1(t *testing.T) {
	cases := []struct {
		name string
		raw  uint16
		want BMSFault1
	}{
		{name: "clear", raw: 0, want: BMSFault1{}},
		{name: "over voltage only", raw: 0x0002, want: BMSFault1{OverVoltage: true, Raw: 0x0002}},
		{name: "under voltage only", raw: 0x0004, want: BMSFault1{UnderVoltage: true, Raw: 0x0004}},
		{name: "over temp only", raw: 0x0008, want: BMSFault1{OverTemp: true, Raw: 0x0008}},
		{name: "under temp only", raw: 0x0010, want: BMSFault1{UnderTemp: true, Raw: 0x0010}},
		{name: "charge over temp only", raw: 0x0020, want: BMSFault1{ChargeOverTemp: true, Raw: 0x0020}},
		{name: "charge under temp only", raw: 0x0040, want: BMSFault1{ChargeUnderTemp: true, Raw: 0x0040}},
		{name: "discharge over current only", raw: 0x0080, want: BMSFault1{DischargeOverCurrent: true, Raw: 0x0080}},
		{
			name: "over voltage with charge over temp",
			raw:  0x0022,
			want: BMSFault1{OverVoltage: true, ChargeOverTemp: true, Raw: 0x0022},
		},
		{name: "reserved bits preserved in Raw only", raw: 0x8001, want: BMSFault1{Raw: 0x8001}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecodeBMSFault1(tc.raw)
			if got != tc.want {
				t.Errorf("DecodeBMSFault1(0x%04X) = %+v, want %+v", tc.raw, got, tc.want)
			}
			if want := tc.raw != 0; got.Any() != want {
				t.Errorf("Any() = %v, want %v", got.Any(), want)
			}
		})
	}
}

func TestDecodeBMSFault2(t *testing.T) {
	cases := []struct {
		name string
		raw  uint16
		want BMSFault2
	}{
		{name: "clear", raw: 0, want: BMSFault2{}},
		{name: "charge over current only", raw: 0x0001, want: BMSFault2{ChargeOverCurrent: true, Raw: 0x0001}},
		{name: "internal only", raw: 0x0008, want: BMSFault2{BMSInternal: true, Raw: 0x0008}},
		{name: "module unbalanced only", raw: 0x0010, want: BMSFault2{ModuleUnbalanced: true, Raw: 0x0010}},
		{
			name: "internal with module unbalanced",
			raw:  0x0018,
			want: BMSFault2{BMSInternal: true, ModuleUnbalanced: true, Raw: 0x0018},
		},
		{name: "reserved bits preserved in Raw only", raw: 0x00E6, want: BMSFault2{Raw: 0x00E6}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecodeBMSFault2(tc.raw)
			if got != tc.want {
				t.Errorf("DecodeBMSFault2(0x%04X) = %+v, want %+v", tc.raw, got, tc.want)
			}
			if want := tc.raw != 0; got.Any() != want {
				t.Errorf("Any() = %v, want %v", got.Any(), want)
			}
		})
	}
}
