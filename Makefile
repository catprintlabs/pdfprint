BINARY := pdfprint
PKG    := ./cmd/pdfprint
STAMP  := stamp

# print-test knobs (override on the command line)
DEVICE   ?= pxlmono
PAGESIZE ?= Legal
FIXTURE  ?= testdata/legal_ruler.pdf

.PHONY: all build windows test vet fmt clean fixture print-test

all: build

build:
	go build -o $(BINARY) $(PKG)
	go build -o $(STAMP) ./cmd/stamp

windows:
	GOOS=windows GOARCH=amd64 go build -o $(BINARY).exe $(PKG)
	GOOS=windows GOARCH=amd64 go build -o $(STAMP).exe ./cmd/stamp

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# One-command smoke test: stamp the ruler fixture with the print command + a
# timestamp, then print the stamped page so the paper documents what produced it.
# Pass a target — a Windows printer name (auto-routes to raw TCP for WSD/V4
# queues) or a raw-TCP host (works on any OS):
#   make print-test PRINTER="Xerox VersaLink C620 (FC:90:A7)"
#   make print-test HOST=10.0.1.151
# The stamped job goes to testdata/smoke.out.pdf (gitignored).
ifdef PRINTER
PT_TARGET := --printer "$(PRINTER)"
else
PT_TARGET := --host $(HOST)
endif

print-test: build
	@test -n "$(PRINTER)$(HOST)" || { echo 'usage: make print-test PRINTER="<name>" | HOST=<ip>'; exit 2; }
	./$(STAMP) --cmd 'pdfprint --scale none --device $(DEVICE) --page-size $(PAGESIZE) $(PT_TARGET) $(FIXTURE)' \
	           --line 'fixture: $(FIXTURE)' -o testdata/smoke.out.pdf $(FIXTURE)
	./$(BINARY) --scale none --device $(DEVICE) --page-size $(PAGESIZE) $(PT_TARGET) testdata/smoke.out.pdf

# Regenerate the sample PDF fixture used for manual end-to-end testing.
fixture:
	gs -q -dBATCH -dNOPAUSE -sDEVICE=pdfwrite -o testdata/hello.pdf -c \
	  "<</PageSize[612 792]>>setpagedevice /Helvetica findfont 36 scalefont setfont \
	   72 700 moveto (CatPrint PDF->PCL test) show \
	   0 0 1 setrgbcolor 72 500 200 120 rectfill \
	   1 0 0 setrgbcolor 100 300 moveto (color box above) show showpage"

clean:
	rm -f $(BINARY) $(BINARY).exe $(STAMP) $(STAMP).exe *.pcl *.ps
