package radio

import "testing"

// D-106: the one rejection line hides two causes, told apart by what hmipserver wrote before it
func TestHmIPFatalExchangeCause(t *testing.T) {
	reject := "de.eq3.cbcs.server.local.base.internal.HMIPTRXInitialResponseListener [vert.x-eventloop-thread-0] Adapter exchange was rejected by key server.\n"
	cases := []struct {
		name, out, cause string
	}{
		{"answered and refused", "Init Hardware Info\nKeyServerCommunicationWorker request sent\n" + reject, CauseRefused},
		{"an HTTP status other than 200", "WARN Frontend Server responded with HTTP Status 503\n" + reject, CauseUnreachable},
		{"no DNS", "java.net.UnknownHostException: secgtw.homematic.com\n\tat java.base/java.net.InetAddress.getAllByName\n" + reject, CauseUnreachable},
		{"no connection", "java.net.ConnectException: Connection refused\n" + reject, CauseUnreachable},
		{"a timeout", "java.net.SocketTimeoutException: connect timed out\n" + reject, CauseUnreachable},
		{"a TLS failure", "javax.net.ssl.SSLHandshakeException: PKIX path building failed\n" + reject, CauseUnreachable},
		// a network line after the rejection belongs to something else
		{"the network line after it", reject + "java.net.ConnectException: later\n", CauseRefused},
	}
	for _, c := range cases {
		code, line, cause := hmipFatal(c.out)
		if code != "adapter-exchange-rejected" || line == "" || cause != c.cause {
			t.Errorf("%s: %q %q %q", c.name, code, line, cause)
		}
	}
	if code, _, cause := hmipFatal("java.net.ConnectException: nothing fatal\nstarted\n"); code != "" || cause != "" {
		t.Errorf("a network line alone is not fatal: %q %q", code, cause)
	}
}
