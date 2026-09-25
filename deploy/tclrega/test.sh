#!/bin/sh
# Exercises tclrega.so the way an addon's session.tcl does, against fake session directories:
# the mirror names a session's file by the SHA-256 of its id (B-102), and the alias mirror an
# alias's file the same way (task 125). Needs cc, tclsh and tcl.h.
set -e
D=$(mktemp -d)
mkdir -p "$D/sessions" "$D/legacy-sessions"
key() { printf '%s' "$1" | sha256sum | cut -d' ' -f1; }
SID=ABCDEFGHIJKLMNOPQRSTUVWXYZ
echo "admin" > "$D/sessions/$(key $SID)"
printf 'admin\n%s\n' "$(key $SID)" > "$D/legacy-sessions/$(key AbCdEfGhIj)"
echo "admin" > "$D/sessions/RAWSESS001"                                # the mirror's old shape: no session
echo "admin" > "$D/sessions/$(key WrongDir01)"                         # an alias in the session directory: none
echo "admin" > "$D/legacy-sessions/$(key WRONGDIRAAAAAAAAAAAAAAAAAA)"  # a session id in the alias directory: none
# build a variant pointing at the temp dir
cc -O2 -Wall -fPIC -shared -DOCCULITE_SESSION_DIR="\"$D/sessions\"" -DOCCULITE_LEGACY_DIR="\"$D/legacy-sessions\"" -I"$(dirname "$(find /usr/include -name tcl.h | head -1)")" -o "$D/tclrega.so" tclrega.c
cat > "$D/t.tcl" <<EOF
load $D/tclrega.so
package require rega
proc check sid {
    if {[regexp {@([0-9a-zA-Z]{10})@} \$sid all sidnr]} {
        set res [lindex [rega_script "Write(system.GetSessionVarStr('\$sidnr'));"] 1]
        if {\$res != ""} { return "user=\$res" }
    }
    return "invalid"
}
proc header sid {
    set res [lindex [rega_script "Write(system.GetSessionVarStr('\$sid'));"] 1]
    if {\$res != ""} { return "header=\$res" }
    return "no-header"
}
puts [check "@AbCdEfGhIj@"]
puts [check "@ZZZZZZZZZZ@"]
puts [check "@WrongDir01@"]
puts [check "garbage"]
puts [header "$SID"]
puts [header "@$SID@"]
puts [header "WRONGDIRAAAAAAAAAAAAAAAAAA"]
puts [header "RAWSESS001"]
puts [catch {rega_script "var x = dom.GetObject(1);"} err]
puts \$err
puts [catch {rega "foo"} err2]
EOF
tclsh "$D/t.tcl" > "$D/out.txt" 2>&1
cat "$D/out.txt"
want='user=admin
invalid
invalid
invalid
header=admin
header=admin
no-header
no-header
1'
[ "$(head -9 "$D/out.txt")" = "$want" ]
grep -q 'openccu-lite without ReGaHSS' "$D/out.txt"
rm -rf "$D"
echo "tclrega.so: OK"
