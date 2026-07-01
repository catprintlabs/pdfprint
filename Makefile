BINARY := pdfprint
PKG    := ./cmd/pdfprint

.PHONY: all build windows test vet fmt clean fixture

all: build

build:
	go build -o $(BINARY) $(PKG)

windows:
	GOOS=windows GOARCH=amd64 go build -o $(BINARY).exe $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Regenerate the sample PDF fixture used for manual end-to-end testing.
fixture:
	gs -q -dBATCH -dNOPAUSE -sDEVICE=pdfwrite -o testdata/hello.pdf -c \
	  "<</PageSize[612 792]>>setpagedevice /Helvetica findfont 36 scalefont setfont \
	   72 700 moveto (CatPrint PDF->PCL test) show \
	   0 0 1 setrgbcolor 72 500 200 120 rectfill \
	   1 0 0 setrgbcolor 100 300 moveto (color box above) show showpage"

clean:
	rm -f $(BINARY) $(BINARY).exe *.pcl *.ps
