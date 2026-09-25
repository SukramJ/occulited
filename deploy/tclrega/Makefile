# Builds the openccu-lite tclrega.so. In the image it is built by the buildroot package with the
# target toolchain; this Makefile is for the host-side test (`make test` needs tclsh and tcl.h).
TCL_INC ?= $(shell for d in /usr/include/tcl8.6 /usr/include/tcl /usr/include; do [ -f $$d/tcl.h ] && echo $$d && break; done)
CFLAGS  ?= -O2 -Wall -Wextra -fPIC
tclrega.so: tclrega.c
	$(CC) $(CFLAGS) -shared -I$(TCL_INC) -o $@ $<
test: tclrega.so
	sh test.sh
clean:
	rm -f tclrega.so
