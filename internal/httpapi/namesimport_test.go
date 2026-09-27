package httpapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/hobbyquaker/occulited/internal/meta"
)

// openccu-lite task 281: the device import takes the names of the same checked backup before its
// reboot - MetaAPI.ImportNamesFromSBK is the regadom import's path for a restore-<file>.sbk, merging
// into the store; a backup without a ReGa database is an error the caller reports and goes on
func TestImportNamesFromSBK(t *testing.T) {
	// the regaimport tests' sample database: two devices, three channels (one with the CCU's default
	// name, which the import leaves out), two rooms, one function
	regadom := `<?xml version="1.0" encoding="iso-8859-1" ?>
<dom>
<interfacemap><count>2</count>
<oid>1035</oid><ifc><obj><id>1035</id><name>BidCos-RF</name><type>458753</type></obj><ifc-url>xmlrpc_bin://127.0.0.1:32001</ifc-url></ifc>
<oid>1036</oid><ifc><obj><id>1036</id><name>HmIP-RF</name><type>458753</type></obj></ifc>
</interfacemap>
<devicemap><count>2</count>
<oid>1555</oid><device><obj><id>1555</id><name>HM-CC-TC JEQ9000001</name><type>17</type><metadata><count>1</count><property>DEVDESC</property><value>[ADDRESS:&quot;JEQ9000001&quot;,CHILDREN:{&quot;JEQ9000001:1&quot;},INTERFACE:&quot;OEQ9000007&quot;,TYPE:&quot;HM-CC-TC&quot;]</value></metadata></obj></device>
<oid>1403</oid><device><obj><id>1403</id><name>Dimmer B&#252;ro</name><type>17</type><metadata><count>1</count><property>DEVDESC</property><value>[ADDRESS:&quot;0001D0000000B2&quot;,CHILDREN:{&quot;0001D0000000B2:3&quot;},INTERFACE:&quot;3014F711A0&quot;]</value></metadata></obj></device>
</devicemap>
<channelmap><count>3</count>
<oid>1575</oid><channel><obj><id>1575</id><name>Wohnzimmer Thermostat</name><type>33</type><metadata><count>2</count><property>AutoconfRoles</property><value>WEATHER</value><property>DEVDESC</property><value>[ADDRESS:&quot;JEQ9000001:1&quot;,PARENT:&quot;JEQ9000001&quot;,TYPE:&quot;WEATHER&quot;]</value></metadata></obj></channel>
<oid>1578</oid><channel><obj><id>1578</id><name>HM-CC-TC JEQ9000001:2</name><type>33</type><metadata><count>1</count><property>DEVDESC</property><value>[ADDRESS:&quot;JEQ9000001:2&quot;,PARENT:&quot;JEQ9000001&quot;]</value></metadata></obj></channel>
<oid>1410</oid><channel><obj><id>1410</id><name>B&#252;ro Licht</name><type>33</type><metadata><count>1</count><property>DEVDESC</property><value>[ADDRESS:&quot;0001D0000000B2:3&quot;,PARENT:&quot;0001D0000000B2&quot;]</value></metadata></obj></channel>
</channelmap>
<hssdpmap><count>2</count>
<oid>1576</oid><dp><obj><id>1576</id><name>BidCos-RF.JEQ9000001:1.HUMIDITY</name><type>393281</type></obj></dp>
<oid>1579</oid><dp><obj><id>1579</id><name>BidCos-RF.JEQ9000001:2.STATE</name><type>393281</type></obj></dp>
<oid>1411</oid><dp><obj><id>1411</id><name>HmIP-RF.0001D0000000B2:3.LEVEL</name><type>393281</type></obj></dp>
</hssdpmap>
<usermap><count>1</count>
<oid>1004</oid><user><obj><id>1004</id><name>Admin</name><type>129</type></obj><userlevel>8</userlevel><name></name><favorite>65535</favorite></user>
</usermap>
<favoritemap><count>3</count>
<oid>202</oid><favorite><enum><obj><id>202</id><name>PC</name><type>10485763</type></obj><entype>6</entype><enel><count>0</count></enel></enum></favorite>
<oid>1033</oid><favorite><enum><obj><id>1033</id><name>_USER1004</name><type>10485763</type></obj><entype>6</entype><enel><count>2</count><oid>1410</oid><ot>33</ot><oid>1575</oid><ot>33</ot></enel></enum></favorite>
<oid>1034</oid><favorite><enum><obj><id>1034</id><name>Wand</name><type>10485763</type></obj><entype>6</entype><enel><count>1</count><oid>1410</oid><ot>33</ot></enel></enum></favorite>
</favoritemap>
<enummap><count>6</count>
<oid>201</oid><enum><obj><id>201</id><name>Favoriten</name><type>3</type></obj><entype>5</entype><enel><count>3</count><oid>202</oid><ot>10485763</ot><oid>1033</oid><ot>10485763</ot><oid>1034</oid><ot>10485763</ot></enel></enum>
<oid>101</oid><enum><obj><id>101</id><name>Rooms</name><type>3</type></obj><entype>1</entype><enel><count>2</count><oid>1019</oid><ot>3</ot><oid>1021</oid><ot>3</ot></enel></enum>
<oid>102</oid><enum><obj><id>102</id><name>Functions</name><type>3</type></obj><entype>1</entype><enel><count>1</count><oid>1301</oid><ot>3</ot></enel></enum>
<oid>1019</oid><enum><obj><id>1019</id><name>Wohnzimmer</name><type>3</type><metadata><count>1</count><property>trID</property><value>roomLivingRoom</value></metadata></obj><entype>2</entype><enel><count>2</count><oid>1575</oid><ot>33</ot><oid>1578</oid><ot>33</ot></enel></enum>
<oid>1021</oid><enum><obj><id>1021</id><name>B&#252;ro</name><type>3</type></obj><entype>2</entype><enel><count>1</count><oid>1410</oid><ot>33</ot></enel></enum>
<oid>1301</oid><enum><obj><id>1301</id><name>Heizung</name><type>3</type></obj><entype>2</entype><enel><count>1</count><oid>1575</oid><ot>33</ot></enel></enum>
</enummap>
</dom>
`
	sbk := func(withRegadom bool) string {
		var inner bytes.Buffer
		gz := gzip.NewWriter(&inner)
		tw := tar.NewWriter(gz)
		_ = tw.WriteHeader(&tar.Header{Name: "usr/local/etc/config/ids", Mode: 0o644, Size: 3})
		_, _ = tw.Write([]byte("abc"))
		if withRegadom {
			_ = tw.WriteHeader(&tar.Header{Name: "usr/local/etc/config/homematic.regadom", Mode: 0o640, Size: int64(len(regadom))})
			_, _ = tw.Write([]byte(regadom))
		}
		_ = tw.Close()
		_ = gz.Close()
		var outer bytes.Buffer
		ow := tar.NewWriter(&outer)
		_ = ow.WriteHeader(&tar.Header{Name: "usr_local.tar.gz", Mode: 0o644, Size: int64(inner.Len())})
		_, _ = ow.Write(inner.Bytes())
		_ = ow.Close()
		p := filepath.Join(t.TempDir(), "restore-x.sbk")
		if err := os.WriteFile(p, outer.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	s, err := meta.New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	after := 0
	a := &MetaAPI{Store: s, AfterImport: func() { after++ }}
	res := a.ImportNamesFromSBK(sbk(true))
	if !res.OK || res.Objects != 4 || res.Rooms != 2 || res.Functions != 1 || !res.Changed || res.Error != "" || after != 1 {
		t.Fatalf("names: %+v after=%d", res, after)
	}
	if o := s.Snapshot().Objects["BidCos-RF.JEQ9000001:1"]; o == nil || o.Name != "Wohnzimmer Thermostat" {
		t.Errorf("the channel's name is not in the store: %+v", o)
	}
	// the same import again: nothing changes, no AfterImport
	if res := a.ImportNamesFromSBK(sbk(true)); !res.OK || res.Changed || after != 1 {
		t.Errorf("second import: %+v after=%d", res, after)
	}
	// without a ReGa database: the error, nothing imported
	if res := a.ImportNamesFromSBK(sbk(false)); res.OK || res.Error == "" {
		t.Errorf("no regadom: %+v", res)
	}
	if res := a.ImportNamesFromSBK(filepath.Join(t.TempDir(), "gone.sbk")); res.OK || res.Error == "" {
		t.Errorf("missing file: %+v", res)
	}
}
