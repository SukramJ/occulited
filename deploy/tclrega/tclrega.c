/*
 * tclrega.so for openccu-lite — a clean-room replacement (D-6, task 6).
 *
 * On a CCU, every addon settings CGI validates the WebUI session with
 *
 *     load tclrega.so
 *     set res [rega_script "Write(system.GetSessionVarStr('$sid'));"]
 *     lindex $res 1        ;# the user name, or "" when the session is invalid
 *
 * That call is the whole reason this library exists. There is no ReGa here and no HM-Script is
 * interpreted (D-1): rega_script recognises exactly that one script form, looks the session up in
 * the directories occulited mirrors on tmpfs, and returns the same Tcl list shape the original
 * returns — {STDOUT <user>} — so the addon's session.tcl never notices the difference. The sid an
 * addon holds is the session's legacy alias (?sid=@xxxxxxxxxx@, task 125) or, for an addon that
 * reads the gate's X-Occulite-Session header, the session id itself; both are answered. Any other
 * script is a Tcl error that names openccu-lite, and a syslog line records who asked, so an
 * addon that needs the real ReGa fails loudly instead of mysteriously.
 *
 * Interface reproduced from the original's documented behaviour (package "rega" 1.1, commands
 * rega_script, rega, rega_url, rega_sid, rega_post); no code from eQ-3's tclrega.cpp (HMSL) was
 * used. AGPL-3.0-or-later.
 *
 * Build: cc -shared -fPIC -O2 -o tclrega.so tclrega.c -I<tcl include dir>  (no libtcl link
 * needed when Tcl stubs are not used: the symbols resolve against tclsh at load time).
 */

#include <errno.h>
#include <regex.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <syslog.h>
#include <tcl.h>

#include "sha256.h"

#ifndef OCCULITE_SESSION_DIR
#define OCCULITE_SESSION_DIR "/var/run/occulite/sessions"
#endif
#ifndef OCCULITE_LEGACY_DIR
#define OCCULITE_LEGACY_DIR "/var/run/occulite/legacy-sessions"
#endif

/* The two shapes a CGI may ask about (task 125): the session id, 26 characters of base32, and the
 * legacy alias, the CCU's ten alphanumerics, which is what an addon's session.tcl extracts with
 * `@([0-9a-zA-Z]{10})@`. Each may come wrapped in @. */
static const char *session_pattern =
    "^[[:space:]]*Write[[:space:]]*\\([[:space:]]*system\\.GetSessionVarStr[[:space:]]*\\("
    "[[:space:]]*['\"]@?([A-Z2-7]{26}|[A-Za-z0-9]{10})@?['\"][[:space:]]*\\)[[:space:]]*\\)[[:space:]]*;?[[:space:]]*$";

/* Looks the credential up in the directory occulited mirrors on tmpfs: one file per live
 * session in OCCULITE_SESSION_DIR, one per live alias in OCCULITE_LEGACY_DIR, each named by the
 * SHA-256 of the id in lower-case hex (B-102: no id is kept anywhere outside occulited's memory),
 * the first line the user name. A 26-character id is looked up among the sessions, a
 * ten-character one among the aliases, never the other way round. No daemon round trip, works
 * even while occulited restarts. */
static int lookup_session(const char *sid, char *user, size_t userlen) {
    char key[65];
    size_t len = strlen(sid);
    occulite_sha256_hex((const unsigned char *)sid, len, key);
    char path[512];
    snprintf(path, sizeof path, "%s/%s", len == 26 ? OCCULITE_SESSION_DIR : OCCULITE_LEGACY_DIR, key);
    FILE *f = fopen(path, "r");
    if (!f) return 0;
    if (!fgets(user, (int)userlen, f)) user[0] = '\0';
    fclose(f);
    size_t n = strlen(user);
    while (n > 0 && (user[n - 1] == '\n' || user[n - 1] == '\r')) user[--n] = '\0';
    return n > 0;
}

static int RegaScriptCmd(ClientData cd, Tcl_Interp *interp, int objc, Tcl_Obj *const objv[]) {
    (void)cd;
    if (objc < 2) {
        Tcl_WrongNumArgs(interp, 1, objv, "script ?script ...?");
        return TCL_ERROR;
    }
    /* the original concatenates its arguments into one script */
    Tcl_DString script;
    Tcl_DStringInit(&script);
    for (int i = 1; i < objc; i++) {
        if (i > 1) Tcl_DStringAppend(&script, "\n", 1);
        Tcl_DStringAppend(&script, Tcl_GetString(objv[i]), -1);
    }
    regex_t re;
    regmatch_t m[2];
    int rc = regcomp(&re, session_pattern, REG_EXTENDED);
    if (rc != 0) {
        Tcl_DStringFree(&script);
        Tcl_SetResult(interp, (char *)"tclrega: internal regex error", TCL_STATIC);
        return TCL_ERROR;
    }
    rc = regexec(&re, Tcl_DStringValue(&script), 2, m, 0);
    regfree(&re);
    if (rc != 0) {
        openlog("tclrega", LOG_PID, LOG_DAEMON);
        syslog(LOG_WARNING, "unsupported ReGa script on openccu-lite (no ReGaHSS here): %.120s", Tcl_DStringValue(&script));
        closelog();
        Tcl_DStringFree(&script);
        Tcl_SetResult(interp, (char *)"tclrega: this system runs openccu-lite without ReGaHSS; only the session check "
                                      "Write(system.GetSessionVarStr('<sid>')) is supported",
                      TCL_STATIC);
        return TCL_ERROR;
    }
    char sid[27];
    size_t n = (size_t)(m[1].rm_eo - m[1].rm_so);
    memcpy(sid, Tcl_DStringValue(&script) + m[1].rm_so, n);
    sid[n] = '\0';
    Tcl_DStringFree(&script);

    char user[128] = "";
    (void)lookup_session(sid, user, sizeof user);

    /* {STDOUT <user>} — an empty user is what the original returns for an invalid session */
    Tcl_Obj *list = Tcl_NewListObj(0, NULL);
    Tcl_ListObjAppendElement(interp, list, Tcl_NewStringObj("STDOUT", -1));
    Tcl_ListObjAppendElement(interp, list, Tcl_NewStringObj(user, -1));
    Tcl_SetObjResult(interp, list);
    return TCL_OK;
}

static int UnsupportedCmd(ClientData cd, Tcl_Interp *interp, int objc, Tcl_Obj *const objv[]) {
    (void)cd;
    (void)objc;
    openlog("tclrega", LOG_PID, LOG_DAEMON);
    syslog(LOG_WARNING, "unsupported command %s on openccu-lite (no ReGaHSS here)", Tcl_GetString(objv[0]));
    closelog();
    Tcl_AppendResult(interp, "tclrega: ", Tcl_GetString(objv[0]),
                     " is not available on openccu-lite (no ReGaHSS); only rega_script with the session check works", NULL);
    return TCL_ERROR;
}

static int RegaSidCmd(ClientData cd, Tcl_Interp *interp, int objc, Tcl_Obj *const objv[]) {
    /* the original stores a sid to append to its HTTP requests; here it is accepted and ignored */
    (void)cd;
    (void)objc;
    (void)objv;
    Tcl_SetResult(interp, (char *)"", TCL_STATIC);
    return TCL_OK;
}

int Tclrega_Init(Tcl_Interp *interp) {
#ifdef USE_TCL_STUBS
    if (Tcl_InitStubs(interp, "8.2", 0) == NULL) return TCL_ERROR;
#endif
    Tcl_CreateObjCommand(interp, "rega_script", RegaScriptCmd, NULL, NULL);
    Tcl_CreateObjCommand(interp, "rega", UnsupportedCmd, NULL, NULL);
    Tcl_CreateObjCommand(interp, "rega_post", UnsupportedCmd, NULL, NULL);
    Tcl_CreateObjCommand(interp, "rega_url", RegaSidCmd, NULL, NULL);
    Tcl_CreateObjCommand(interp, "rega_sid", RegaSidCmd, NULL, NULL);
    Tcl_SetVar(interp, "rega_version", "1.1", TCL_GLOBAL_ONLY);
    return Tcl_PkgProvide(interp, "rega", "1.1");
}
