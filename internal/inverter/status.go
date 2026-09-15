package inverter

import "fmt"

// statusLabels maps the inverter status enum (input register 33095) to the text
// the vendor's protocol document lists in its "all 4G" display column
// (Appendix 2). This unit reads 3 — "Generating" — in normal operation; the
// entries that matter operationally are the 0x20xx communications faults, where
// CAN_Comm_FAIL, Alarm-BMS and Alarm2-BMS are how a battery problem surfaces.
var statusLabels = map[uint16]string{
	0x0000: "Waiting",
	0x0001: "OpenRun",
	0x0002: "SoftRun",
	0x0003: "Generating",
	0x0004: "Standby",
	0x0005: "StandbySynoch",
	0x0006: "GridToLoad",
	0x000F: "Normal",
	0x1004: "Grid Off",

	// Grid faults.
	0x1010: "OV-G-V",
	0x1011: "UN-G-V",
	0x1012: "OV-G-F",
	0x1013: "UN-G-F",
	0x1014: "Reve-Grid",
	0x1015: "NO-Grid",
	0x1016: "G-PHASE",
	0x1017: "G-F-FLU",
	0x1018: "OV-G-I",
	0x1019: "IGFOL-F",

	// DC-side faults.
	0x1020: "OV-DC",
	0x1021: "OV-BUS",
	0x1022: "UNB-BUS",
	0x1023: "UN-BUS",
	0x1024: "UNB2-BUS",
	0x1025: "OV-DCA-I",
	0x1026: "OV-DCB-I",
	0x1027: "DC-INTF.",
	0x1028: "Reve-DC",
	0x1029: "PvMidIso",

	// Protection faults.
	0x1030: "GRID-INTF.",
	0x1031: "INI-FAULT",
	0x1032: "OV-TEM",
	0x1033: "PV ISO-PRO",
	0x1034: "ILeak-PRO",
	0x1035: "RelayChk-FAIL",
	0x1036: "DSP-B-FAULT",
	0x1037: "DCInj-FAULT",
	0x1038: "12Power-FAULT",
	0x1039: "ILeak-Check",
	0x103A: "UN-TEM",

	// AFCI and DSP self-check faults.
	0x1040: "AFCI-Check",
	0x1041: "ARC-FAULT",
	0x1042: "RAM-FAULT",
	0x1043: "FLASH-FAULT",
	0x1044: "PC-FAULT",
	0x1045: "REG-FAULT",
	0x1046: "GRID-INTF02",
	0x1047: "IG-AD",
	0x1048: "IGBT-OV-I",

	// Battery and backup faults.
	0x1050: "OV-IgTr",
	0x1051: "OV-Vbatt-H",
	0x1052: "OV-ILLC",
	0x1053: "OV-Vbatt",
	0x1054: "UN-Vbatt",
	0x1055: "NO-Battery",
	0x1056: "OV-VBackup",
	0x1057: "Over-Load",
	0x1058: "DspSelfChk",

	// Communications faults.
	0x2010: "Fail Safe",
	0x2011: "MET_Comm_FAIL",
	0x2012: "CAN_Comm_FAIL",
	0x2014: "DSP_Comm_FAIL",
	0x2015: "Alarm-BMS",
	0x2016: "BatName-FAIL",
	0x2017: "Alarm2-BMS",
	0x2018: "DRM_LINK_FAIL",
	0x2019: "MET_SEL_FAIL",
	0x2020: "HighTemp.AMB",
	0x2021: "LowTemp.AMB",

	// Alarms.
	0xF010: "Surge Alarm",
	0xF011: "Fan Alarm",
}

// StatusLabel renders the 33095 status enum as its vendor display text. An
// unlisted code falls back to its hex form ("0xBEEF") rather than to a guess, so
// an unknown status is legible and never mislabelled.
func StatusLabel(code uint16) string {
	if label, ok := statusLabels[code]; ok {
		return label
	}
	return fmt.Sprintf("0x%04X", code)
}
