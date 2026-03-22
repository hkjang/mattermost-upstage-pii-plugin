package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// maskRegion represents a rectangular area on a specific page that should be masked.
type maskRegion struct {
	PageNumber int
	Polygon    [4][2]float64
}

// pageSize holds the dimensions of a document page as reported by the API.
type pageSize struct {
	Width  float64
	Height float64
}

// collectMaskRegions traverses the API response payload and collects bounding
// boxes for entities whose keys match allowedKeys.  An allowedKeys entry of
// "*" or "all" matches every entity.  Matching uses prefix comparison so that
// "개인정보.주소" also matches "개인정보.주소.상세주소".
func collectMaskRegions(payload any, allowedKeys []string) []maskRegion {
	if len(allowedKeys) == 0 {
		return nil
	}
	matchAll := false
	for _, k := range allowedKeys {
		if k == "*" || strings.EqualFold(k, "all") {
			matchAll = true
			break
		}
	}
	var regions []maskRegion
	collectMaskRegionsRecursive(payload, allowedKeys, matchAll, &regions)
	return regions
}

func collectMaskRegionsRecursive(value any, allowedKeys []string, matchAll bool, regions *[]maskRegion) {
	switch typed := value.(type) {
	case map[string]any:
		key := strings.TrimSpace(stringValue(typed["key"]))
		if key != "" && (matchAll || matchesPIIKeyPrefix(key, allowedKeys)) {
			bboxes := parseBoundingBoxes(typed["boundingBoxes"])
			pageNum := 1
			if pn, ok := intValue(typed["pageNumber"]); ok && pn > 0 {
				pageNum = pn
			}
			for _, poly := range bboxes {
				*regions = append(*regions, maskRegion{
					PageNumber: pageNum,
					Polygon:    poly,
				})
			}
		}
		for _, nested := range typed {
			collectMaskRegionsRecursive(nested, allowedKeys, matchAll, regions)
		}
	case []any:
		for _, item := range typed {
			collectMaskRegionsRecursive(item, allowedKeys, matchAll, regions)
		}
	}
}

func matchesPIIKeyPrefix(key string, allowedKeys []string) bool {
	normalized := strings.TrimSpace(key)
	for _, allowed := range allowedKeys {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if strings.HasPrefix(normalized, allowed) {
			return true
		}
	}
	return false
}

// parseBoundingBoxes converts the raw boundingBoxes field from the API into
// typed polygon arrays.  Each polygon is 4 points of [x, y].
//
// Supported formats:
//   - UFP polygon:  [[[x1,y1],[x2,y2],[x3,y3],[x4,y4]]]
//   - OAC rect obj: [{"x":10,"y":20,"width":100,"height":30}]
//   - Flat array:   [[x1,y1,x2,y2,x3,y3,x4,y4]]
//   - Vertices:     [{"vertices":[{"x":10,"y":20},{"x":110,"y":20},...]}]
func parseBoundingBoxes(raw any) [][4][2]float64 {
	arr, ok := raw.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	var result [][4][2]float64
	for _, item := range arr {
		if poly, ok := parsePolygonPoints(item); ok {
			result = append(result, poly)
			continue
		}
		if poly, ok := parseRectObject(item); ok {
			result = append(result, poly)
			continue
		}
		if poly, ok := parseFlatCoords(item); ok {
			result = append(result, poly)
			continue
		}
		if poly, ok := parseVerticesObject(item); ok {
			result = append(result, poly)
			continue
		}
	}
	return result
}

// parsePolygonPoints handles [[x1,y1],[x2,y2],[x3,y3],[x4,y4]]
func parsePolygonPoints(item any) ([4][2]float64, bool) {
	polyArr, ok := item.([]any)
	if !ok || len(polyArr) != 4 {
		return [4][2]float64{}, false
	}
	var poly [4][2]float64
	for i, ptRaw := range polyArr {
		ptArr, ok := ptRaw.([]any)
		if !ok || len(ptArr) != 2 {
			return [4][2]float64{}, false
		}
		x, xOK := toFloat64(ptArr[0])
		y, yOK := toFloat64(ptArr[1])
		if !xOK || !yOK {
			return [4][2]float64{}, false
		}
		poly[i] = [2]float64{x, y}
	}
	return poly, true
}

// parseRectObject handles {"x":10,"y":20,"width":100,"height":30}
func parseRectObject(item any) ([4][2]float64, bool) {
	m, ok := item.(map[string]any)
	if !ok {
		return [4][2]float64{}, false
	}
	x, xOK := rectFloat(m, "x")
	y, yOK := rectFloat(m, "y")
	if !xOK || !yOK {
		return [4][2]float64{}, false
	}
	w, wOK := rectFloat(m, "width", "w")
	h, hOK := rectFloat(m, "height", "h")
	if !wOK || !hOK {
		return [4][2]float64{}, false
	}
	return [4][2]float64{
		{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h},
	}, true
}

func rectFloat(m map[string]any, keys ...string) (float64, bool) {
	for _, k := range keys {
		if v, ok := toFloat64(m[k]); ok {
			return v, true
		}
	}
	return 0, false
}

// parseFlatCoords handles [x1,y1,x2,y2,x3,y3,x4,y4]
func parseFlatCoords(item any) ([4][2]float64, bool) {
	arr, ok := item.([]any)
	if !ok || len(arr) != 8 {
		return [4][2]float64{}, false
	}
	var poly [4][2]float64
	for i := 0; i < 4; i++ {
		x, xOK := toFloat64(arr[i*2])
		y, yOK := toFloat64(arr[i*2+1])
		if !xOK || !yOK {
			return [4][2]float64{}, false
		}
		poly[i] = [2]float64{x, y}
	}
	return poly, true
}

// parseVerticesObject handles {"vertices":[{"x":10,"y":20},{"x":110,"y":20},...]}
func parseVerticesObject(item any) ([4][2]float64, bool) {
	m, ok := item.(map[string]any)
	if !ok {
		return [4][2]float64{}, false
	}
	verts, ok := m["vertices"].([]any)
	if !ok || len(verts) != 4 {
		return [4][2]float64{}, false
	}
	var poly [4][2]float64
	for i, v := range verts {
		vm, ok := v.(map[string]any)
		if !ok {
			return [4][2]float64{}, false
		}
		x, xOK := toFloat64(vm["x"])
		y, yOK := toFloat64(vm["y"])
		if !xOK || !yOK {
			return [4][2]float64{}, false
		}
		poly[i] = [2]float64{x, y}
	}
	return poly, true
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// extractPageSizes traverses the payload to find pageSizes arrays
// (typically in UFP responses under documents.<idx>.pageSizes).
// Returns a map from 1-based page number to page dimensions.
func extractPageSizes(payload any) map[int]pageSize {
	sizes := map[int]pageSize{}
	extractPageSizesRecursive(payload, sizes)
	return sizes
}

func extractPageSizesRecursive(value any, sizes map[int]pageSize) {
	switch typed := value.(type) {
	case map[string]any:
		if psRaw, ok := typed["pageSizes"]; ok {
			if psArr, ok := psRaw.([]any); ok {
				for i, item := range psArr {
					if m, ok := item.(map[string]any); ok {
						w, wOK := toFloat64(m["width"])
						h, hOK := toFloat64(m["height"])
						if wOK && hOK && w > 0 && h > 0 {
							sizes[i+1] = pageSize{Width: w, Height: h}
						}
					}
				}
			}
		}
		for _, nested := range typed {
			extractPageSizesRecursive(nested, sizes)
		}
	case []any:
		for _, item := range typed {
			extractPageSizesRecursive(item, sizes)
		}
	}
}

// polygonBounds computes the axis-aligned bounding rectangle from a 4-point polygon.
func polygonBounds(poly [4][2]float64) (minX, minY, maxX, maxY float64) {
	minX, minY = poly[0][0], poly[0][1]
	maxX, maxY = minX, minY
	for _, pt := range poly[1:] {
		minX = math.Min(minX, pt[0])
		minY = math.Min(minY, pt[1])
		maxX = math.Max(maxX, pt[0])
		maxY = math.Max(maxY, pt[1])
	}
	return
}

// maskImageFile decodes an image, draws black rectangles over the specified
// regions, and re-encodes it in the original format.
func maskImageFile(content []byte, mimeType string, regions []maskRegion, pageSizes map[int]pageSize) ([]byte, error) {
	img, format, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	bounds := img.Bounds()
	imgW := float64(bounds.Dx())
	imgH := float64(bounds.Dy())

	// Determine coordinate scale factor.
	scaleX, scaleY := 1.0, 1.0
	if ps, ok := pageSizes[1]; ok && ps.Width > 0 && ps.Height > 0 {
		scaleX = imgW / ps.Width
		scaleY = imgH / ps.Height
	}

	// Create a mutable copy of the image.
	dst := image.NewRGBA(bounds)
	draw.Draw(dst, bounds, img, bounds.Min, draw.Src)

	black := image.NewUniform(color.Black)
	for _, region := range regions {
		minX, minY, maxX, maxY := polygonBounds(region.Polygon)
		rect := image.Rect(
			int(minX*scaleX),
			int(minY*scaleY),
			int(math.Ceil(maxX*scaleX)),
			int(math.Ceil(maxY*scaleY)),
		)
		draw.Draw(dst, rect, black, image.Point{}, draw.Src)
	}

	var buf bytes.Buffer
	switch {
	case format == "png" || strings.Contains(mimeType, "png"):
		err = png.Encode(&buf, dst)
	default:
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 95})
	}
	if err != nil {
		return nil, fmt.Errorf("failed to encode masked image: %w", err)
	}
	return buf.Bytes(), nil
}

// maskPDFFile adds black rectangles to the specified pages of a PDF document.
func maskPDFFile(content []byte, regions []maskRegion, pageSizes map[int]pageSize) ([]byte, error) {
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed

	// Get page dimensions from the PDF.
	pdfPageDims, err := api.PageDims(bytes.NewReader(content), conf)
	if err != nil {
		return nil, fmt.Errorf("failed to read PDF page dimensions: %w", err)
	}

	ctx, err := api.ReadContext(bytes.NewReader(content), conf)
	if err != nil {
		return nil, fmt.Errorf("failed to read PDF: %w", err)
	}
	if err := ctx.EnsurePageCount(); err != nil {
		return nil, fmt.Errorf("failed to get PDF page count: %w", err)
	}

	// Group regions by page.
	byPage := map[int][]maskRegion{}
	for _, r := range regions {
		byPage[r.PageNumber] = append(byPage[r.PageNumber], r)
	}

	for pageNum, pageRegions := range byPage {
		if pageNum < 1 || pageNum > ctx.PageCount {
			continue
		}

		// PDF page dimensions (in points, 1-indexed).
		var pdfW, pdfH float64
		if pageNum <= len(pdfPageDims) {
			pdfW = pdfPageDims[pageNum-1].Width
			pdfH = pdfPageDims[pageNum-1].Height
		} else {
			pdfW = 612
			pdfH = 792
		}

		// Determine scale from API coordinates to PDF coordinates.
		apiW, apiH := pdfW, pdfH
		if ps, ok := pageSizes[pageNum]; ok && ps.Width > 0 && ps.Height > 0 {
			apiW = ps.Width
			apiH = ps.Height
		}
		scaleX := pdfW / apiW
		scaleY := pdfH / apiH

		// Build PDF content stream with black rectangles.
		var sb strings.Builder
		sb.WriteString("q\n0 0 0 rg\n")
		for _, region := range pageRegions {
			minX, minY, maxX, maxY := polygonBounds(region.Polygon)
			// Convert from top-left origin to PDF bottom-left origin.
			pdfX := minX * scaleX
			pdfY := pdfH - maxY*scaleY
			w := (maxX - minX) * scaleX
			h := (maxY - minY) * scaleY
			sb.WriteString(fmt.Sprintf("%.2f %.2f %.2f %.2f re f\n", pdfX, pdfY, w, h))
		}
		sb.WriteString("Q\n")

		err = addContentStreamToPage(ctx, pageNum, sb.String())
		if err != nil {
			return nil, fmt.Errorf("failed to add mask to PDF page %d: %w", pageNum, err)
		}
	}

	var buf bytes.Buffer
	if err := api.WriteContext(ctx, &buf); err != nil {
		return nil, fmt.Errorf("failed to write masked PDF: %w", err)
	}
	return buf.Bytes(), nil
}

// addContentStreamToPage appends a content stream to an existing PDF page.
func addContentStreamToPage(ctx *model.Context, pageNum int, content string) error {
	pgDict, _, _, err := ctx.PageDict(pageNum, false)
	if err != nil {
		return err
	}

	sd := &types.StreamDict{
		Dict: types.Dict(map[string]types.Object{
			"Length": types.Integer(len(content)),
		}),
		Content: []byte(content),
	}
	sd.InsertName("Filter", "")

	ref, err := ctx.IndRefForNewObject(*sd)
	if err != nil {
		return err
	}

	// Get current Contents entry and build an array including the new stream.
	contentsEntry, found := pgDict.Find("Contents")
	var arr types.Array
	if found && contentsEntry != nil {
		switch v := contentsEntry.(type) {
		case types.Array:
			arr = v
		default:
			arr = types.Array{v}
		}
	}
	arr = append(arr, *ref)
	pgDict["Contents"] = arr
	return nil
}

// isImageMIME returns true for image MIME types that can be masked.
func isImageMIME(mime string) bool {
	mime = strings.ToLower(mime)
	for _, prefix := range []string{"image/png", "image/jpeg", "image/jpg", "image/bmp", "image/tiff"} {
		if strings.HasPrefix(mime, prefix) {
			return true
		}
	}
	return false
}

// isPDFMIME returns true for PDF MIME types.
func isPDFMIME(mime string) bool {
	return strings.Contains(strings.ToLower(mime), "pdf")
}

// maskedFilename generates a filename for the masked version of a file.
func maskedFilename(original string) string {
	return "masked_" + original
}

// maskAndUploadFiles masks PII regions in attachments and uploads them to Mattermost.
func (p *Plugin) maskAndUploadFiles(results []upstageDocumentResult, channelID string, allowedKeys []string) ([]string, error) {
	if len(allowedKeys) == 0 {
		return nil, nil
	}

	p.API.LogInfo("maskAndUploadFiles: starting", "files", len(results), "allowedKeys", strings.Join(allowedKeys, ","))

	var fileIDs []string
	for _, result := range results {
		payload := parsePIIResultPayload(result.Response.Result)
		// Fallback to full response body.
		if payload == nil && strings.TrimSpace(result.ResponseDebug.Body) != "" {
			payload = parseDebugPayloadString(result.ResponseDebug.Body)
		}
		if payload == nil {
			p.API.LogWarn("maskAndUploadFiles: no parseable payload", "file", result.Attachment.Name)
			continue
		}

		regions := collectMaskRegions(payload, allowedKeys)
		if len(regions) == 0 {
			p.API.LogInfo("maskAndUploadFiles: no matching regions", "file", result.Attachment.Name)
			continue
		}

		p.API.LogInfo("maskAndUploadFiles: masking", "file", result.Attachment.Name, "mime", result.Attachment.MIMEType, "regions", len(regions))

		pageSizes := extractPageSizes(payload)
		mime := result.Attachment.MIMEType

		var maskedContent []byte
		var err error
		switch {
		case isImageMIME(mime):
			maskedContent, err = maskImageFile(result.Attachment.Content, mime, regions, pageSizes)
		case isPDFMIME(mime):
			maskedContent, err = maskPDFFile(result.Attachment.Content, regions, pageSizes)
		default:
			continue
		}
		if err != nil {
			return fileIDs, fmt.Errorf("failed to mask %s: %w", result.Attachment.Name, err)
		}

		fileInfo, appErr := p.API.UploadFile(maskedContent, channelID, maskedFilename(result.Attachment.Name))
		if appErr != nil {
			return fileIDs, fmt.Errorf("failed to upload masked file %s: %w", result.Attachment.Name, appErr)
		}
		fileIDs = append(fileIDs, fileInfo.Id)
	}
	return fileIDs, nil
}
