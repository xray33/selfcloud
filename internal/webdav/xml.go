package webdav

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"selfcloud/internal/domain"
)

// MultiStatus is the top-level WebDAV 207 Multi-Status XML response.
type MultiStatus struct {
	XMLName   xml.Name   `xml:"d:multistatus"`
	XmlnsD    string     `xml:"xmlns:d,attr"`
	XmlnsOC   string     `xml:"xmlns:oc,attr"`
	Responses []Response `xml:"d:response"`
}

// Response is a single DAV:response element inside Multi-Status.
type Response struct {
	HRef     string   `xml:"d:href"`
	PropStat PropStat `xml:"d:propstat"`
}

// PropStat groups a prop set and its HTTP status.
type PropStat struct {
	Prop   Prop   `xml:"d:prop"`
	Status string `xml:"d:status"`
}

// Prop holds all WebDAV and ownCloud-specific properties returned per resource.
type Prop struct {
	GetLastModified  string        `xml:"d:getlastmodified,omitempty"`
	GetContentLength *int64        `xml:"d:getcontentlength,omitempty"`
	ResourceType     *ResourceType `xml:"d:resourcetype"`
	GetETag          string        `xml:"d:getetag,omitempty"`
	GetContentType   string        `xml:"d:getcontenttype,omitempty"`
	// ownCloud-specific properties — required for client to work correctly.
	OCFileID      string `xml:"oc:fileid,omitempty"`
	OCSize        *int64 `xml:"oc:size,omitempty"`
	OCID          string `xml:"oc:id,omitempty"`
	OCPermissions string `xml:"oc:permissions,omitempty"`
}

// ResourceType represents d:resourcetype. For directories it contains d:collection.
type ResourceType struct {
	Collection *struct{} `xml:"d:collection,omitempty"`
}

// FormatMultiStatus encodes a slice of nodes into a WebDAV 207 Multi-Status XML body.
// basePath is the WebDAV root prefix used to compute d:href values.
func FormatMultiStatus(basePath string, nodes []*domain.Node) ([]byte, error) {
	ms := MultiStatus{
		XmlnsD:  "DAV:",
		XmlnsOC: "http://owncloud.org/ns",
	}

	for _, node := range nodes {
		// Build the href, URL-encoding the path so non-ASCII filenames work.
		var href string
		if node.Path == "/" {
			href = basePath + "/"
		} else {
			// Encode each path segment individually to preserve slashes.
			href = basePath + encodePath(node.Path)
		}
		if node.IsDir && len(href) > 0 && href[len(href)-1] != '/' {
			href += "/"
		}

		// Full permissions string: ownCloud expects these letters:
		// R = read, D = delete, NV = rename+move, W = write (update)
		// CK = create file+folder  — give all for now (per-user ACL can be added later).
		permissions := "RDNVWCK"

		prop := Prop{
			GetLastModified: node.MTime.UTC().Format(http.TimeFormat),
			GetETag:         fmt.Sprintf(`"%s"`, node.ETag),
			OCFileID:        node.FileID,
			OCID:            node.FileID,
			OCPermissions:   permissions,
		}

		if node.IsDir {
			prop.ResourceType = &ResourceType{Collection: &struct{}{}}
		} else {
			prop.ResourceType = &ResourceType{}
			size := node.Size
			prop.GetContentLength = &size
			prop.OCSize = &size
			if node.ContentType != "" {
				prop.GetContentType = node.ContentType
			} else {
				prop.GetContentType = "application/octet-stream"
			}
		}

		resp := Response{
			HRef: href,
			PropStat: PropStat{
				Prop:   prop,
				Status: "HTTP/1.1 200 OK",
			},
		}

		ms.Responses = append(ms.Responses, resp)
	}

	output, err := xml.MarshalIndent(ms, "", "  ")
	if err != nil {
		return nil, err
	}

	return append([]byte(xml.Header), output...), nil
}

// encodePath URL-encodes a slash-separated path preserving slashes.
func encodePath(p string) string {
	// Split on "/" and encode each segment, then rejoin.
	// url.PathEscape encodes everything except unreserved chars; it encodes spaces as %20.
	var b strings.Builder
	b.Grow(len(p) + 16)
	start := 0
	for i, c := range p {
		if c == '/' {
			b.WriteString(url.PathEscape(p[start:i]))
			b.WriteByte('/')
			start = i + 1
		}
	}
	b.WriteString(url.PathEscape(p[start:]))
	return b.String()
}
