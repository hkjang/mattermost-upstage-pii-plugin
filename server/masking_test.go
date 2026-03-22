package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCollectMaskRegionsMatchesAllKeys(t *testing.T) {
	payload := parsePayloadJSON(t, `{
		"fields": [
			{
				"key": "개인정보.이름",
				"value": "홍길동",
				"boundingBoxes": [[[10,20],[100,20],[100,50],[10,50]]]
			},
			{
				"key": "개인정보.주민등록번호",
				"value": "900101-1XXXXXX",
				"boundingBoxes": [[[10,60],[200,60],[200,90],[10,90]]]
			}
		]
	}`)

	regions := collectMaskRegions(payload, []string{"*"})
	require.Len(t, regions, 2)
	require.Equal(t, 1, regions[0].PageNumber)
}

func TestCollectMaskRegionsFiltersKeys(t *testing.T) {
	payload := parsePayloadJSON(t, `{
		"fields": [
			{
				"key": "개인정보.이름",
				"value": "홍길동",
				"boundingBoxes": [[[10,20],[100,20],[100,50],[10,50]]]
			},
			{
				"key": "개인정보.주민등록번호",
				"value": "900101-1XXXXXX",
				"boundingBoxes": [[[10,60],[200,60],[200,90],[10,90]]]
			}
		]
	}`)

	regions := collectMaskRegions(payload, []string{"개인정보.이름"})
	require.Len(t, regions, 1)
	require.InDelta(t, 10.0, regions[0].Polygon[0][0], 0.01)
}

func TestCollectMaskRegionsPrefixMatch(t *testing.T) {
	payload := parsePayloadJSON(t, `{
		"fields": [
			{
				"key": "개인정보.주소",
				"value": "서울시",
				"boundingBoxes": [[[0,0],[10,0],[10,10],[0,10]]]
			},
			{
				"key": "개인정보.주소.상세주소",
				"value": "강남구",
				"boundingBoxes": [[[20,0],[30,0],[30,10],[20,10]]]
			}
		]
	}`)

	regions := collectMaskRegions(payload, []string{"개인정보.주소"})
	require.Len(t, regions, 2, "prefix match should catch both 주소 and 주소.상세주소")
}

func TestCollectMaskRegionsEmptyKeys(t *testing.T) {
	payload := parsePayloadJSON(t, `{
		"fields": [
			{
				"key": "개인정보.이름",
				"value": "홍길동",
				"boundingBoxes": [[[10,20],[100,20],[100,50],[10,50]]]
			}
		]
	}`)

	regions := collectMaskRegions(payload, nil)
	require.Empty(t, regions)

	regions = collectMaskRegions(payload, []string{})
	require.Empty(t, regions)
}

func TestCollectMaskRegionsUFPStructure(t *testing.T) {
	payload := parsePayloadJSON(t, `{
		"documents": {
			"0": {
				"groups": [
					{
						"entities": [
							{
								"key": "개인정보.이름",
								"value": "홍길동",
								"boundingBoxes": [[[120,45],[280,45],[280,78],[120,78]]],
								"pageNumber": 1
							}
						]
					}
				],
				"pageSizes": [{"width": 800, "height": 600}]
			}
		}
	}`)

	regions := collectMaskRegions(payload, []string{"*"})
	require.Len(t, regions, 1)
	require.Equal(t, 1, regions[0].PageNumber)
	require.InDelta(t, 120.0, regions[0].Polygon[0][0], 0.01)
}

func TestExtractPageSizes(t *testing.T) {
	payload := parsePayloadJSON(t, `{
		"documents": {
			"0": {
				"pageSizes": [
					{"width": 800, "height": 600},
					{"width": 1200, "height": 900}
				]
			}
		}
	}`)

	sizes := extractPageSizes(payload)
	require.Len(t, sizes, 2)
	require.InDelta(t, 800.0, sizes[1].Width, 0.01)
	require.InDelta(t, 600.0, sizes[1].Height, 0.01)
	require.InDelta(t, 1200.0, sizes[2].Width, 0.01)
}

func TestMaskImageFile(t *testing.T) {
	// Create a 100x100 white PNG image.
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.White)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))

	regions := []maskRegion{
		{PageNumber: 1, Polygon: [4][2]float64{{10, 10}, {50, 10}, {50, 30}, {10, 30}}},
	}

	masked, err := maskImageFile(buf.Bytes(), "image/png", regions, nil)
	require.NoError(t, err)
	require.NotEmpty(t, masked)

	// Decode the masked image and verify the masked area is black.
	maskedImg, _, err := image.Decode(bytes.NewReader(masked))
	require.NoError(t, err)

	// Center of the masked rectangle should be black.
	r, g, b, _ := maskedImg.At(30, 20).RGBA()
	require.Equal(t, uint32(0), r)
	require.Equal(t, uint32(0), g)
	require.Equal(t, uint32(0), b)

	// Outside the rectangle should still be white.
	r, g, b, _ = maskedImg.At(0, 0).RGBA()
	require.Equal(t, uint32(0xffff), r)
	require.Equal(t, uint32(0xffff), g)
	require.Equal(t, uint32(0xffff), b)
}

func TestMaskImageFileWithPageSizeScaling(t *testing.T) {
	// Create a 200x200 white image but API coords use 100x100 space.
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 200; x++ {
			img.Set(x, y, color.White)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))

	regions := []maskRegion{
		{PageNumber: 1, Polygon: [4][2]float64{{0, 0}, {50, 0}, {50, 50}, {0, 50}}},
	}
	pageSizesMap := map[int]pageSize{1: {Width: 100, Height: 100}}

	masked, err := maskImageFile(buf.Bytes(), "image/png", regions, pageSizesMap)
	require.NoError(t, err)

	maskedImg, _, err := image.Decode(bytes.NewReader(masked))
	require.NoError(t, err)

	// With 2x scale, (0,0)-(50,50) maps to (0,0)-(100,100) in image space.
	// Point (50,50) in image should be black.
	r, g, b, _ := maskedImg.At(50, 50).RGBA()
	require.Equal(t, uint32(0), r)
	require.Equal(t, uint32(0), g)
	require.Equal(t, uint32(0), b)

	// Point (150,150) should be white (outside masked area).
	r, g, b, _ = maskedImg.At(150, 150).RGBA()
	require.Equal(t, uint32(0xffff), r)
}

func TestMatchesPIIKeyPrefix(t *testing.T) {
	require.True(t, matchesPIIKeyPrefix("개인정보.이름", []string{"개인정보.이름"}))
	require.True(t, matchesPIIKeyPrefix("개인정보.주소.상세주소", []string{"개인정보.주소"}))
	require.False(t, matchesPIIKeyPrefix("개인정보.이름", []string{"개인정보.주소"}))
	require.False(t, matchesPIIKeyPrefix("개인정보.이름", []string{}))
}

func TestDetectPIIResultSchemaUFP(t *testing.T) {
	payload := parsePayloadJSON(t, `{"documents": {"0": {"groups": []}}}`)
	require.Equal(t, "ufp", detectPIIResultSchema(payload))
}

func TestPolygonBounds(t *testing.T) {
	poly := [4][2]float64{{10, 20}, {100, 20}, {100, 50}, {10, 50}}
	minX, minY, maxX, maxY := polygonBounds(poly)
	require.InDelta(t, 10.0, minX, 0.01)
	require.InDelta(t, 20.0, minY, 0.01)
	require.InDelta(t, 100.0, maxX, 0.01)
	require.InDelta(t, 50.0, maxY, 0.01)
}

func parsePayloadJSON(t *testing.T, raw string) any {
	t.Helper()
	var payload any
	require.NoError(t, json.Unmarshal([]byte(raw), &payload))
	return payload
}
