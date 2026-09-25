package unitshow

import (
	"strings"
	"testing"
)

func TestWords(t *testing.T) {
	// the After= of usr-local.mount on the Charly (captured 2026-09-12): systemctl quotes the names with a backslash
	v := `system.slice -.mount "blockdev@dev-disk-by\\x2dlabel-userfs.target" "systemd-fsck@dev-disk-by\\x2dlabel-userfs.service" dev-mmcblk0p3.device`
	got := strings.Join(Words(v), "|")
	want := `system.slice|-.mount|blockdev@dev-disk-by\x2dlabel-userfs.target|systemd-fsck@dev-disk-by\x2dlabel-userfs.service|dev-mmcblk0p3.device`
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	if got := strings.Join(Words(` a  'b c' "d\"e" `), "|"); got != `a|b c|d"e` {
		t.Errorf("quotes: %s", got)
	}
	if len(Words("")) != 0 || len(Words("   ")) != 0 {
		t.Error("nothing")
	}
}

func TestBlocks(t *testing.T) {
	out := "UserspaceTimestampMonotonic=6380673\nFinishTimestampMonotonic=73643085\n\n" +
		"Id=rfd.service\nAfter=multimacd.service  network.target \nActiveEnterTimestampMonotonic=37100000\nDescription=BidCos-RF = the radio\n\n\n" +
		"Id=hmipserver.service\nActiveEnterTimestampMonotonic=\nInactiveExitTimestampMonotonic=-3\n" +
		"no equals sign here\n\n" +
		"After=" + strings.Repeat("x.service ", 10000) + "\n"
	blocks := Blocks([]byte(out))
	if len(blocks) != 4 {
		t.Fatalf("%d blocks", len(blocks))
	}
	if blocks[0]["Id"] != "" || Micros(blocks[0], "FinishTimestampMonotonic") != 73643085 {
		t.Errorf("manager: %v", blocks[0])
	}
	if blocks[1]["After"] != "multimacd.service  network.target" || blocks[1]["Description"] != "BidCos-RF = the radio" {
		t.Errorf("values: %v", blocks[1])
	}
	if len(blocks[3]["After"]) < 90000 {
		t.Error("a long line is read whole")
	}
	byID := ByID([]byte(out))
	if len(byID) != 2 || Micros(byID["rfd.service"], "ActiveEnterTimestampMonotonic") != 37100000 {
		t.Errorf("by id: %v", byID)
	}
	h := byID["hmipserver.service"]
	if Micros(h, "ActiveEnterTimestampMonotonic") != 0 || Micros(h, "InactiveExitTimestampMonotonic") != 0 || Micros(h, "Missing") != 0 {
		t.Errorf("unset, negative, missing: %v", h)
	}
	if len(Blocks(nil)) != 0 {
		t.Error("nothing")
	}
}
