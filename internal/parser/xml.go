package parser

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
)

func newZipReader(data []byte) (*zip.Reader, error) {
	return zip.NewReader(bytes.NewReader(data), int64(len(data)))
}

func extractXMLText(r io.Reader) (string, error) {
	decoder := xml.NewDecoder(r)
	var buf strings.Builder
	var inParagraph bool

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return buf.String(), nil
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "p" {
				inParagraph = true
			}
		case xml.EndElement:
			if t.Name.Local == "p" && inParagraph {
				buf.WriteString("\n")
				inParagraph = false
			}
		case xml.CharData:
			buf.Write(t)
		}
	}
	return buf.String(), nil
}
