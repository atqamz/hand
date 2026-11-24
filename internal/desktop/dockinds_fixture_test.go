package desktop_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

const (
	salesCSV   = "reports/t1/sales.csv"
	salesTSV   = "reports/t1/sales.tsv"
	bigCSV     = "reports/t1/events.csv"
	voicePath  = "reports/t1/standup.mp3"
	clipPath   = "reports/t1/demo.mp4"
	pagePath   = "reports/t1/preview.html"
	planPath   = "reports/t1/plan.pdf"
	emptyCSV   = "reports/t1/empty.csv"
	quotedCSV  = "reports/t1/quoted.csv"
	raggedCSV  = "reports/t1/ragged.csv"
	wideCSV    = "reports/t1/wide.csv"
	headerCSV  = "reports/t1/header-only.csv"
	bomCSV     = "reports/t1/bom.csv"
	tableRows  = 2000
	pageSource = "<!doctype html>\n<html>\n<head>\n  <title>Export preview</title>\n</head>\n<body>\n  <h1>Nightly export</h1>\n  <p>One manifest per region.</p>\n  <script>run()</script>\n</body>\n</html>"
)

const salesBody = "Region,Orders,Revenue,Note\nnorth,1204,48210.50,steady\nsouth,988,39120.00,\"promo, week 2\"\neast,1530,61004.75,record night\nwest,76,2210.10,\"outage at 02:10\"\ncentral,412,16500.00,\n"

const salesTSVBody = "Region\tOrders\tRevenue\tNote\nnorth\t1204\t48210.50\tsteady\nsouth\t988\t39120.00\tpromo, week 2\neast\t1530\t61004.75\trecord night\nwest\t76\t2210.10\toutage at 02:10\ncentral\t412\t16500.00\t\n"

func kindsFixture(t *testing.T) *fixture {
	t.Helper()
	f := docFixture(t)
	f.docWindowSize = 500
	size := func(b string) int64 { return int64(len(b)) }
	table := func(path, kind, body string) {
		name := path[strings.LastIndex(path, "/")+1:]
		f.docs[path] = desktop.Doc{Path: path, Name: name, Kind: kind, Text: true, Size: size(body), At: at("1"), FolderPage: 1}
		f.docBytes[path] = []byte(body)
		lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
		if body == "" {
			lines = nil
		}
		f.docLines[path] = lines
	}
	table(salesCSV, "csv", salesBody)
	table(salesTSV, "tsv", salesTSVBody)
	table(emptyCSV, "csv", "")
	table(headerCSV, "csv", "id,name,state\n")
	table(quotedCSV, "csv", "id,note\r\n1,\"first line\nsecond line\"\r\n2,\"says \"\"hi\"\", then leaves\"\r\n")
	table(raggedCSV, "csv", "a,b,c\n1\n1,2,3,4,5\n\n")
	table(bomCSV, "csv", "\ufeffname,count\nalpha,1\n")
	table(wideCSV, "csv", "id,text\n1,"+strings.Repeat("0123456789", 60)+"TAILMARK\n")
	f.docs[bigCSV] = desktop.Doc{Path: bigCSV, Name: "events.csv", Kind: "csv", Text: true, Size: 61 << 10, At: at("1"), FolderPage: 1}
	f.docLines[bigCSV] = eventRows(tableRows + 500)
	f.docBytes[planPath] = []byte("%PDF-1.7 fixture")
	for path, kind := range map[string]string{voicePath: "audio", clipPath: "video"} {
		name := path[strings.LastIndex(path, "/")+1:]
		f.docs[path] = desktop.Doc{Path: path, Name: name, Kind: kind, Size: 3 << 20, At: at("1"), FolderPage: 1}
		f.docBytes[path] = []byte("RIFF" + kind)
	}
	f.docs[pagePath] = desktop.Doc{Path: pagePath, Name: "preview.html", Kind: "html", Text: true, Size: size(pageSource), At: at("1"), FolderPage: 1}
	f.docBytes[pagePath] = []byte(pageSource)
	f.docLines[pagePath] = strings.Split(pageSource, "\n")
	return f
}

func eventRows(n int) []string {
	lines := []string{"id,kind,detail"}
	for i := 1; i <= n; i++ {
		lines = append(lines, fmt.Sprintf("%d,export,row %d", i, i))
	}
	return lines
}

func eventsFixture(t *testing.T, rows int) *fixture {
	t.Helper()
	f := kindsFixture(t)
	f.docLines[bigCSV] = eventRows(rows)
	return f
}
