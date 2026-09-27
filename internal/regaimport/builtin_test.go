package regaimport

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/meta"
)

func TestBuiltinName(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"roomBathroom", "Badezimmer", true},
		{"${roomBathroom}", "Badezimmer", true},
		{"roomKitchen", "Küche", true},
		{"${roomOffice}", "Büro", true},
		{"roomChildrensRoom2", "Kinderzimmer 2", true},
		{"funcHeating", "Heizung", true},
		{"${funcCentral}", "Zentrale", true},
		{"funcEnergy", "Energiemanagement", true},
		// a name a user typed, or anything that is not exactly a key: left as it is
		{"Badezimmer", "Badezimmer", false},
		{"roombathroom", "roombathroom", false},
		{"roomBathroom 2", "roomBathroom 2", false},
		{"Mein roomBathroom", "Mein roomBathroom", false},
		{" roomBathroom", " roomBathroom", false},
		{"${roomBathroom", "${roomBathroom", false},
		{"roomBathroom}", "roomBathroom}", false},
		{"$roomBathroom", "$roomBathroom", false},
		{"${${roomBathroom}}", "${${roomBathroom}}", false},
		{"${}", "${}", false},
		{"${sysVarPresence}", "${sysVarPresence}", false},
		{"roomAttic", "roomAttic", false},
		{"", "", false},
	} {
		got, ok := BuiltinName(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("BuiltinName(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// The table is what hm_startup creates: eleven rooms, ten functions, and every name one the store
// accepts in both languages.
func TestBuiltinTable(t *testing.T) {
	rooms, funcs := 0, 0
	for key, n := range builtinNames {
		switch {
		case strings.HasPrefix(key, "room"):
			rooms++
		case strings.HasPrefix(key, "func"):
			funcs++
		default:
			t.Errorf("%q is neither a room nor a function key", key)
		}
		for _, name := range []string{n.de, n.en} {
			if got, err := meta.NormalizeName(name); err != nil || got != name {
				t.Errorf("%s: %q is not a valid name as it stands: %v", key, name, err)
			}
			if _, ok := BuiltinName(name); ok {
				t.Errorf("%s: the translation %q is itself a key", key, name)
			}
		}
	}
	if rooms != 11 || funcs != 10 {
		t.Errorf("%d rooms, %d functions; the CCU creates 11 and 10", rooms, funcs)
	}
}

// a regadom as a CCU leaves it: the built-in rooms and functions under their keys, bare (what a
// CCU3 database holds) and in substitution form, next to a room the user created
func builtinRegadom() []byte {
	return []byte(`<?xml version="1.0" encoding="iso-8859-1" ?>
<dom>
<interfacemap><count>1</count>
<oid>1035</oid><ifc><obj><id>1035</id><name>BidCos-RF</name><type>458753</type></obj></ifc>
</interfacemap>
<devicemap><count>1</count>
<oid>1555</oid><device><obj><id>1555</id><name>Thermostat Bad</name><type>17</type><metadata><count>1</count><property>DEVDESC</property><value>[ADDRESS:&quot;JEQ9000001&quot;,CHILDREN:{&quot;JEQ9000001:1&quot;}]</value></metadata></obj></device>
</devicemap>
<channelmap><count>2</count>
<oid>1575</oid><channel><obj><id>1575</id><name>Bad Klima</name><type>33</type><metadata><count>1</count><property>DEVDESC</property><value>[ADDRESS:&quot;JEQ9000001:1&quot;,PARENT:&quot;JEQ9000001&quot;]</value></metadata></obj></channel>
<oid>1578</oid><channel><obj><id>1578</id><name>Bad Licht</name><type>33</type><metadata><count>1</count><property>DEVDESC</property><value>[ADDRESS:&quot;JEQ9000001:2&quot;,PARENT:&quot;JEQ9000001&quot;]</value></metadata></obj></channel>
</channelmap>
<hssdpmap><count>2</count>
<oid>1576</oid><dp><obj><id>1576</id><name>BidCos-RF.JEQ9000001:1.HUMIDITY</name><type>393281</type></obj></dp>
<oid>1579</oid><dp><obj><id>1579</id><name>BidCos-RF.JEQ9000001:2.STATE</name><type>393281</type></obj></dp>
</hssdpmap>
<enummap><count>8</count>
<oid>101</oid><enum><obj><id>101</id><name>Rooms</name><type>3</type></obj><entype>1</entype><enel><count>3</count><oid>1230</oid><ot>3</ot><oid>1231</oid><ot>3</ot><oid>1232</oid><ot>3</ot></enel></enum>
<oid>102</oid><enum><obj><id>102</id><name>Functions</name><type>3</type></obj><entype>1</entype><enel><count>2</count><oid>1240</oid><ot>3</ot><oid>1241</oid><ot>3</ot></enel></enum>
<oid>1230</oid><enum><obj><id>1230</id><name>roomBathroom</name><type>3</type></obj><entype>2</entype><enel><count>2</count><oid>1575</oid><ot>33</ot><oid>1578</oid><ot>33</ot></enel></enum>
<oid>1231</oid><enum><obj><id>1231</id><name>${roomKitchen}</name><type>3</type></obj><entype>2</entype><enel><count>0</count></enel></enum>
<oid>1232</oid><enum><obj><id>1232</id><name>Hobbyraum roomGarage</name><type>3</type></obj><entype>2</entype><enel><count>0</count></enel></enum>
<oid>1240</oid><enum><obj><id>1240</id><name>${funcHeating}</name><type>3</type></obj><entype>2</entype><enel><count>1</count><oid>1575</oid><ot>33</ot></enel></enum>
<oid>1241</oid><enum><obj><id>1241</id><name>funcLight</name><type>3</type></obj><entype>2</entype><enel><count>1</count><oid>1578</oid><ot>33</ot></enel></enum>
</enummap>
</dom>
`)
}

func treeNames(nodes []*meta.Node) string {
	var s []string
	for _, n := range nodes {
		s = append(s, n.ID+"="+n.Name)
	}
	return strings.Join(s, ", ")
}

func TestConvertTranslatesBuiltins(t *testing.T) {
	fromFile, err := ParseRegadom(bytes.NewReader(builtinRegadom()))
	if err != nil {
		t.Fatal(err)
	}
	// the live script answers with the same names
	fromScript, err := Parse("I\t1035\tBidCos-RF\n" +
		"O\t1575\t1035\tJEQ9000001:1\tBad Klima\nO\t1578\t1035\tJEQ9000001:2\tBad Licht\n" +
		"R\t1230\troomBathroom\nM\t1230\t1575\nM\t1230\t1578\nR\t1231\t${roomKitchen}\nR\t1232\tHobbyraum roomGarage\n" +
		"F\t1240\t${funcHeating}\nM\t1240\t1575\nF\t1241\tfuncLight\nM\t1241\t1578\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		source string
		dump   *Dump
	}{{"regadom", fromFile}, {"script", fromScript}} {
		res := Convert(c.dump, meta.Defaults())
		doc := res.Document
		if err := doc.Validate(); err != nil {
			t.Fatalf("%s: %v", c.source, err)
		}
		if got, want := treeNames(doc.Enums["room"].Tree), "badezimmer=Badezimmer, kueche=Küche, hobbyraum-roomgarage=Hobbyraum roomGarage"; got != want {
			t.Errorf("%s: rooms %s, want %s", c.source, got, want)
		}
		if got, want := treeNames(doc.Enums["function"].Tree), "heizung=Heizung, licht=Licht"; got != want {
			t.Errorf("%s: functions %s, want %s", c.source, got, want)
		}
		if o := doc.Objects["BidCos-RF.JEQ9000001:1"]; o == nil || strings.Join(o.Enums, ",") != "function/heizung,room/badezimmer" {
			t.Errorf("%s: memberships %+v", c.source, o)
		}
		if res.Rooms != 3 || res.Functions != 2 || len(res.Renamed) != 0 {
			t.Errorf("%s: %+v", c.source, res)
		}
	}

	// a store renamed in place keeps its old ids: importing the same CCU again finds those nodes by
	// their translated name instead of adding a second Badezimmer beside them
	base := meta.Defaults()
	base["room"].Tree = []*meta.Node{{ID: "roombathroom", Name: "Badezimmer"}}
	base["function"].Tree = []*meta.Node{{ID: "funcheating", Name: "Heizung"}}
	res := Convert(fromFile, base)
	if got, want := treeNames(res.Document.Enums["room"].Tree), "roombathroom=Badezimmer, kueche=Küche, hobbyraum-roomgarage=Hobbyraum roomGarage"; got != want {
		t.Errorf("merge: rooms %s, want %s", got, want)
	}
	if o := res.Document.Objects["BidCos-RF.JEQ9000001:1"]; o == nil || strings.Join(o.Enums, ",") != "function/funcheating,room/roombathroom" {
		t.Errorf("merge: memberships %+v", o)
	}
}

// what an import from before the table left in the store, next to what a user made
func keyStore(t *testing.T, saver func(*meta.Document) error) *meta.Store {
	t.Helper()
	doc := meta.NewDocument()
	doc.Enums["room"].Tree = []*meta.Node{
		{ID: "roombathroom", Name: "roomBathroom"},
		{ID: "roomkitchen", Name: "${roomKitchen}"},
		{ID: "eg", Name: "EG", Children: []*meta.Node{{ID: "roomoffice", Name: "roomOffice"}, {ID: "flur", Name: "Flur"}}},
		{ID: "wohnzimmer", Name: "Wohnzimmer"},
		{ID: "bad-2", Name: "roomBathroom 2"},
		{ID: "mein-bad", Name: "Mein roomBathroom"},
	}
	doc.Enums["function"].Tree = []*meta.Node{{ID: "funcheating", Name: "funcHeating"}, {ID: "garten", Name: "Garten"}}
	doc.Enums["floor"] = &meta.Enum{Name: map[string]string{"de": "Etagen", "en": "Floors"}, Tree: []*meta.Node{{ID: "garage", Name: "roomGarage"}}}
	doc.Objects["BidCos-RF.JEQ9000001:1"] = &meta.Object{Name: "roomBathroom", Enums: []string{"function/funcheating", "room/eg/roomoffice", "room/roombathroom"}}
	s, err := meta.New(doc, saver)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTranslateBuiltinNodes(t *testing.T) {
	s := keyStore(t, nil)
	ch := s.Subscribe()
	defer s.Unsubscribe(ch)
	renamed, err := TranslateBuiltinNodes(s)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"room/roombathroom: roomBathroom -> Badezimmer",
		"room/roomkitchen: ${roomKitchen} -> Küche",
		"room/eg/roomoffice: roomOffice -> Büro",
		"function/funcheating: funcHeating -> Heizung",
	}
	if strings.Join(renamed, "\n") != strings.Join(want, "\n") {
		t.Errorf("renamed:\n%s\nwant:\n%s", strings.Join(renamed, "\n"), strings.Join(want, "\n"))
	}
	snap := s.Snapshot()
	if got, want := treeNames(snap.Enums["room"].Tree), "roombathroom=Badezimmer, roomkitchen=Küche, eg=EG, wohnzimmer=Wohnzimmer, bad-2=roomBathroom 2, mein-bad=Mein roomBathroom"; got != want {
		t.Errorf("rooms %s, want %s", got, want)
	}
	if got := treeNames(snap.Enums["room"].Tree[2].Children); got != "roomoffice=Büro, flur=Flur" {
		t.Errorf("nested: %s", got)
	}
	if got := treeNames(snap.Enums["function"].Tree); got != "funcheating=Heizung, garten=Garten" {
		t.Errorf("functions: %s", got)
	}
	// another enum, and an object's name, are not the importer's rooms and functions
	if got := treeNames(snap.Enums["floor"].Tree); got != "garage=roomGarage" {
		t.Errorf("other enum touched: %s", got)
	}
	o := snap.Objects["BidCos-RF.JEQ9000001:1"]
	if o.Name != "roomBathroom" || strings.Join(o.Enums, ",") != "function/funcheating,room/eg/roomoffice,room/roombathroom" {
		t.Errorf("object touched: %+v", o)
	}
	// the store's own mutation path: one revision and one node.updated per rename
	if s.Revision() != 4 {
		t.Errorf("revision %d, want 4", s.Revision())
	}
	for i := range want {
		select {
		case e := <-ch:
			if e.Kind != "node.updated" || e.Revision != uint64(i+1) {
				t.Errorf("event %d: %+v", i, e)
			}
		default:
			t.Fatalf("event %d missing", i)
		}
	}

	// idempotent: the second start finds nothing and writes nothing
	again, err := TranslateBuiltinNodes(s)
	if err != nil || len(again) != 0 || s.Revision() != 4 {
		t.Errorf("second run: %v %v revision %d", again, err, s.Revision())
	}

	// a store without the default enums (deleted by the user) is not an error
	empty, _ := meta.New(nil, nil)
	if _, _, err := empty.DeleteEnum(nil, "room", false); err != nil {
		t.Fatal(err)
	}
	if got, err := TranslateBuiltinNodes(empty); err != nil || len(got) != 0 {
		t.Errorf("no room enum: %v %v", got, err)
	}
}

// a rename the store cannot persist stops the pass and leaves the store as it was
func TestTranslateBuiltinNodesSaveError(t *testing.T) {
	s := keyStore(t, func(*meta.Document) error { return errors.New("disk full") })
	renamed, err := TranslateBuiltinNodes(s)
	if err == nil || !strings.Contains(err.Error(), "room/roombathroom") || len(renamed) != 0 {
		t.Errorf("%v %v", renamed, err)
	}
	if s.Revision() != 0 || s.Snapshot().Enums["room"].Tree[0].Name != "roomBathroom" {
		t.Error("store changed")
	}
}
