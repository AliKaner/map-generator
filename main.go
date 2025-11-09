package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type tileSpec struct {
	W     int
	H     int
	Count float64
}

type tileBatch struct {
	W     int
	H     int
	Count int
}

type generator struct {
	width            int
	height           int
	mode             string
	rings            int
	ringStartFrac    float64
	ringEndFrac      float64
	islands          int
	islandRFrac      float64
	rnd              *rand.Rand
	islandCenters    []image.Point
	continentCenters []image.Point
	ringBoundaries   []float64
	totalArea        float64
	sumX             float64
	sumY             float64
}

type mapRequest struct {
	W           int      `json:"w"`
	H           int      `json:"h"`
	Tiles       string   `json:"tiles"`
	Ka          *float64 `json:"ka"`
	Cap         *int     `json:"cap"`
	Mode        string   `json:"mode"`
	Rings       *int     `json:"rings"`
	RingStart   *float64 `json:"ringStart"`
	RingEnd     *float64 `json:"ringEnd"`
	Seed        string   `json:"seed"`
	LogTone     *int     `json:"logTone"`
	BrownCap    *int     `json:"brownCap"`
	BgAlpha     *int     `json:"bgA"`
	Islands     *int     `json:"islands"`
	IslandRFrac *float64 `json:"islandRFrac"`
	Rotate      *int     `json:"rot"`
	N22         *int     `json:"n22"`
	N21         *int     `json:"n21"`
	N11         *int     `json:"n11"`
}

type generationParams struct {
	width       int
	height      int
	tileString  string
	ka          float64
	cap         int
	mode        string
	rings       int
	ringStart   float64
	ringEnd     float64
	seed        string
	logTone     bool
	brownCap    int
	bgAlpha     int
	islands     int
	islandRFrac float64
	rotate      bool
	n22         int
	n21         int
	n11         int
}

type generationResult struct {
	imageData       []byte
	batches         int
	totalPlacements int
	seedValue       int64
}

func newGenerator(width, height int, mode string, rings int, ringStartFrac, ringEndFrac float64, islands int, islandRFrac float64, rnd *rand.Rand) *generator {
	g := &generator{
		width:         width,
		height:        height,
		mode:          strings.ToLower(mode),
		rings:         rings,
		ringStartFrac: ringStartFrac,
		ringEndFrac:   ringEndFrac,
		islands:       islands,
		islandRFrac:   islandRFrac,
		rnd:           rnd,
	}

	switch g.mode {
	case "adalar":
		g.initIslands()
	case "iki-kita":
		g.initContinents()
	case "merkez":
		g.initMerkezRings()
	}

	return g
}

func (g *generator) initIslands() {
	count := g.islands
	if count <= 0 {
		count = 3
	}
	g.islandCenters = make([]image.Point, 0, count)
	minDim := float64(min(g.width, g.height))
	margin := int(minDim * 0.1)
	for i := 0; i < count; i++ {
		x := margin + g.rnd.Intn(max(1, g.width-2*margin))
		y := margin + g.rnd.Intn(max(1, g.height-2*margin))
		g.islandCenters = append(g.islandCenters, image.Point{X: x, Y: y})
	}
}

func (g *generator) initContinents() {
	g.continentCenters = []image.Point{
		{X: g.width / 4, Y: g.height / 2},
		{X: (3 * g.width) / 4, Y: g.height / 2},
	}
}

func (g *generator) initMerkezRings() {
	start := clampFloat(g.ringStartFrac, 0, 1)
	end := clampFloat(g.ringEndFrac, 0, 1)
	if end <= start {
		if end >= 1 {
			start = clampFloat(end-0.1, 0, 1)
		} else {
			end = clampFloat(start+0.1, 0, 1)
		}
	}

	segments := max(1, g.rings)
	g.ringStartFrac = start
	g.ringEndFrac = end
	g.ringBoundaries = make([]float64, segments+1)
	g.ringBoundaries[0] = 0

	if segments == 1 {
		g.ringBoundaries[1] = clampFloat(end, 0, 1)
		if g.ringBoundaries[1] < start {
			g.ringBoundaries[1] = start
		}
		return
	}

	span := end - start
	if span <= 0 {
		span = 0.1
		end = clampFloat(start+span, start, 1)
	}
	step := span / float64(segments-1)

	prev := 0.0
	for i := 1; i <= segments; i++ {
		var val float64
		if i == 1 {
			val = start
		} else if i == segments {
			val = end
		} else {
			val = start + float64(i-1)*step
		}
		val = clampFloat(val, prev, 1)
		g.ringBoundaries[i] = val
		prev = val
	}
}

func (g *generator) positionForTile(tw, th int) (int, int) {
	if tw >= g.width || th >= g.height {
		return 0, 0
	}

	switch g.mode {
	case "merkez":
		return g.positionMerkez(tw, th)
	case "agirlik":
		return g.positionAgirlik(tw, th)
	case "adalar":
		return g.positionAdalar(tw, th)
	case "iki-kita":
		return g.positionIkiKita(tw, th)
	default:
		return g.positionAgirlik(tw, th)
	}
}

func (g *generator) randomPlacement(tw, th int) (int, int) {
	spanX := g.width - tw
	spanY := g.height - th
	if spanX < 0 {
		spanX = 0
	}
	if spanY < 0 {
		spanY = 0
	}

	x := 0
	y := 0
	if spanX > 0 {
		x = g.rnd.Intn(spanX + 1)
	}
	if spanY > 0 {
		y = g.rnd.Intn(spanY + 1)
	}
	return x, y
}

func (g *generator) selectMerkezSegment() (int, bool) {
	segments := len(g.ringBoundaries) - 1
	if segments <= 0 {
		return -1, false
	}

	baseProbs := []float64{0.40, 0.20, 0.10, 0.05}
	limit := min(segments, len(baseProbs))

	totalAssigned := 0.0
	r := g.rnd.Float64()
	cumulative := 0.0
	for i := 0; i < limit; i++ {
		cumulative += baseProbs[i]
		totalAssigned += baseProbs[i]
		if r < cumulative {
			return i, true
		}
	}

	if totalAssigned >= 1.0 {
		if segments > limit {
			return segments - 1, true
		}
		if limit > 0 {
			return limit - 1, true
		}
		return 0, true
	}

	return -1, false
}

func (g *generator) positionMerkez(tw, th int) (int, int) {
	minDim := float64(min(g.width, g.height))
	radiusMax := minDim / 2

	for attempt := 0; attempt < 12; attempt++ {
		segment, useRing := g.selectMerkezSegment()
		if !useRing {
			return g.randomPlacement(tw, th)
		}
		if segment < 0 || segment+1 >= len(g.ringBoundaries) {
			continue
		}

		innerFrac := g.ringBoundaries[segment]
		outerFrac := g.ringBoundaries[segment+1]
		if outerFrac <= innerFrac {
			continue
		}

		radiusFrac := innerFrac + g.rnd.Float64()*(outerFrac-innerFrac)
		theta := g.rnd.Float64() * 2 * math.Pi
		radius := radiusFrac * radiusMax
		cx := float64(g.width)/2 + math.Cos(theta)*radius
		cy := float64(g.height)/2 + math.Sin(theta)*radius
		x := clampInt(int(math.Round(cx))-tw/2, 0, g.width-tw)
		y := clampInt(int(math.Round(cy))-th/2, 0, g.height-th)
		return x, y
	}

	return g.randomPlacement(tw, th)
}

func (g *generator) positionAgirlik(tw, th int) (int, int) {
	targetX := float64(g.width) / 2
	targetY := float64(g.height) / 2

	centerX := clampInt(int(math.Round(targetX))-tw/2, 0, g.width-tw)
	centerY := clampInt(int(math.Round(targetY))-th/2, 0, g.height-th)
	bestX := centerX
	bestY := centerY
	bestScore := g.distanceAfterPlacement(centerX, centerY, tw, th, targetX, targetY)

	currentDist := math.Inf(1)
	if cx, cy, ok := g.centerOfMass(); ok {
		currentDist = math.Hypot(cx-targetX, cy-targetY)
		mirrorCenterX := targetX*2 - cx
		mirrorCenterY := targetY*2 - cy
		mirrorX := clampInt(int(math.Round(mirrorCenterX))-tw/2, 0, g.width-tw)
		mirrorY := clampInt(int(math.Round(mirrorCenterY))-th/2, 0, g.height-th)
		mirrorScore := g.distanceAfterPlacement(mirrorX, mirrorY, tw, th, targetX, targetY)
		if mirrorScore < bestScore {
			bestScore = mirrorScore
			bestX = mirrorX
			bestY = mirrorY
		}
	}

	attempts := 24
	for attempt := 0; attempt < attempts; attempt++ {
		x, y := g.randomPlacement(tw, th)
		score := g.distanceAfterPlacement(x, y, tw, th, targetX, targetY)
		if score < bestScore {
			bestScore = score
			bestX = x
			bestY = y
			if currentDist != math.Inf(1) && score <= currentDist*0.7 {
				break
			}
		}
	}

	return bestX, bestY
}

func (g *generator) recordPlacement(x, y, tw, th int) {
	area := float64(tw * th)
	if area <= 0 {
		return
	}
	centerX := float64(x) + float64(tw)/2
	centerY := float64(y) + float64(th)/2
	g.totalArea += area
	g.sumX += centerX * area
	g.sumY += centerY * area
}

func (g *generator) centerOfMass() (float64, float64, bool) {
	if g.totalArea <= 0 {
		return 0, 0, false
	}
	return g.sumX / g.totalArea, g.sumY / g.totalArea, true
}

func (g *generator) distanceAfterPlacement(x, y, tw, th int, targetX, targetY float64) float64 {
	area := float64(tw * th)
	if area <= 0 {
		if cx, cy, ok := g.centerOfMass(); ok {
			return math.Hypot(cx-targetX, cy-targetY)
		}
		return 0
	}
	total := g.totalArea + area
	tileCenterX := float64(x) + float64(tw)/2
	tileCenterY := float64(y) + float64(th)/2
	newCx := (g.sumX + tileCenterX*area) / total
	newCy := (g.sumY + tileCenterY*area) / total
	dx := newCx - targetX
	dy := newCy - targetY
	return math.Hypot(dx, dy)
}

func (g *generator) positionAdalar(tw, th int) (int, int) {
	if len(g.islandCenters) == 0 {
		return g.positionMerkez(tw, th)
	}
	center := g.islandCenters[g.rnd.Intn(len(g.islandCenters))]
	radiusFrac := g.islandRFrac
	if radiusFrac <= 0 {
		radiusFrac = 0.25
	}
	maxRadius := radiusFrac * float64(min(g.width, g.height))
	radius := g.rnd.Float64() * maxRadius
	theta := g.rnd.Float64() * 2 * math.Pi

	cx := float64(center.X) + math.Cos(theta)*radius
	cy := float64(center.Y) + math.Sin(theta)*radius
	x := clampInt(int(math.Round(cx))-tw/2, 0, g.width-tw)
	y := clampInt(int(math.Round(cy))-th/2, 0, g.height-th)
	return x, y
}

func (g *generator) positionIkiKita(tw, th int) (int, int) {
	if len(g.continentCenters) == 0 {
		return g.positionMerkez(tw, th)
	}
	center := g.continentCenters[g.rnd.Intn(len(g.continentCenters))]
	sigmaX := float64(g.width) / 10
	sigmaY := float64(g.height) / 6
	for attempt := 0; attempt < 6; attempt++ {
		x := int(math.Round(float64(center.X) + g.rnd.NormFloat64()*sigmaX))
		y := int(math.Round(float64(center.Y) + g.rnd.NormFloat64()*sigmaY))
		if x >= 0 && x <= g.width-tw && y >= 0 && y <= g.height-th {
			return x, y
		}
	}
	return g.positionMerkez(tw, th)
}

func parseTileList(input string) ([]tileSpec, error) {
	if strings.TrimSpace(input) == "" {
		return []tileSpec{
			{W: 2, H: 2, Count: 400},
			{W: 2, H: 1, Count: 300},
			{W: 1, H: 1, Count: 100},
		}, nil
	}

	raw := strings.Split(input, ",")
	specs := make([]tileSpec, 0, len(raw))

	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		dimCount := strings.SplitN(part, "*", 2)
		dims := dimCount[0]
		count := 1.0
		if len(dimCount) == 2 {
			clean := strings.TrimSpace(dimCount[1])
			if clean != "" {
				v, err := strconv.ParseFloat(clean, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid tile count in %q: %w", part, err)
				}
				count = v
			}
		}

		dParts := strings.SplitN(dims, "x", 2)
		if len(dParts) != 2 {
			return nil, fmt.Errorf("invalid tile dimensions in %q", part)
		}

		w, err := strconv.Atoi(strings.TrimSpace(dParts[0]))
		if err != nil {
			return nil, fmt.Errorf("invalid tile width in %q: %w", part, err)
		}
		h, err := strconv.Atoi(strings.TrimSpace(dParts[1]))
		if err != nil {
			return nil, fmt.Errorf("invalid tile height in %q: %w", part, err)
		}
		if w <= 0 || h <= 0 {
			return nil, fmt.Errorf("tile dimensions must be positive in %q", part)
		}
		if count <= 0 {
			continue
		}

		specs = append(specs, tileSpec{W: w, H: h, Count: count})
	}

	if len(specs) == 0 {
		return nil, errors.New("no valid tile definitions found")
	}

	return specs, nil
}

func applyLegacyTiles(specs []tileSpec, n22, n21, n11 int) []tileSpec {
	legacy := []struct {
		W, H int
		N    int
	}{
		{2, 2, n22},
		{2, 1, n21},
		{1, 1, n11},
	}

	for _, entry := range legacy {
		if entry.N <= 0 {
			continue
		}
		found := false
		for i := range specs {
			if specs[i].W == entry.W && specs[i].H == entry.H {
				specs[i].Count += float64(entry.N)
				found = true
				break
			}
		}
		if !found {
			specs = append(specs, tileSpec{
				W:     entry.W,
				H:     entry.H,
				Count: float64(entry.N),
			})
		}
	}

	return specs
}

func activateMultiplier(specs []tileSpec, ka float64) {
	if ka == 0 {
		return
	}
	if ka < 0 {
		ka = 0
	}
	if ka == 1 {
		return
	}
	for i := range specs {
		specs[i].Count *= ka
	}
}

func finalizeTileBatches(specs []tileSpec, capLimit int) []tileBatch {
	type fractional struct {
		index int
		frac  float64
	}

	sumCounts := 0.0
	for _, s := range specs {
		sumCounts += s.Count
	}

	if sumCounts == 0 {
		return nil
	}

	scale := 1.0
	if capLimit > 0 && sumCounts > float64(capLimit) {
		scale = float64(capLimit) / sumCounts
	}

	scaledTotals := make([]float64, len(specs))
	floors := make([]int, len(specs))
	fractions := make([]fractional, 0, len(specs))
	totalFloors := 0

	for i, s := range specs {
		adjusted := s.Count * scale
		if adjusted <= 0 {
			continue
		}
		scaledTotals[i] = adjusted
		base := int(math.Floor(adjusted))
		floors[i] = base
		totalFloors += base

		f := adjusted - float64(base)
		if f > 0 {
			fractions = append(fractions, fractional{index: i, frac: f})
		}
	}

	targetTotal := 0
	sumScaled := 0.0
	for _, v := range scaledTotals {
		sumScaled += v
	}

	if capLimit > 0 {
		if scale < 1 {
			targetTotal = capLimit
		} else {
			targetTotal = min(capLimit, int(math.Round(sumScaled)))
		}
	} else {
		targetTotal = int(math.Round(sumScaled))
	}

	if targetTotal < totalFloors {
		// Reduce counts from smallest fractional values first
		sort.Slice(fractions, func(i, j int) bool {
			if fractions[i].frac == fractions[j].frac {
				return fractions[i].index < fractions[j].index
			}
			return fractions[i].frac < fractions[j].frac
		})
		diff := totalFloors - targetTotal
		for k := 0; k < diff && k < len(fractions); k++ {
			idx := fractions[k].index
			if floors[idx] > 0 {
				floors[idx]--
			}
		}
		totalFloors -= min(diff, len(fractions))
	}

	if totalFloors < targetTotal {
		remaining := targetTotal - totalFloors
		sort.Slice(fractions, func(i, j int) bool {
			if fractions[i].frac == fractions[j].frac {
				return fractions[i].index < fractions[j].index
			}
			return fractions[i].frac > fractions[j].frac
		})
		for k := 0; k < remaining && k < len(fractions); k++ {
			floors[fractions[k].index]++
		}
	}

	batches := make([]tileBatch, 0, len(specs))
	for i, s := range specs {
		count := floors[i]
		if count <= 0 {
			continue
		}
		batches = append(batches, tileBatch{
			W:     s.W,
			H:     s.H,
			Count: count,
		})
	}

	return batches
}

func coverageToColor(coverage int, brownCap int, logTone bool, green, brown color.RGBA) color.RGBA {
	if coverage <= 0 {
		return color.RGBA{R: 0, G: 0, B: 0, A: 0}
	}
	if coverage == 1 {
		return green
	}

	if brownCap <= 0 {
		brownCap = 1
	}

	var ratio float64
	if logTone {
		ratio = math.Log(float64(coverage)) / math.Log(float64(brownCap)+1)
	} else {
		ratio = float64(coverage-1) / float64(brownCap)
	}
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}

	return blendColor(green, brown, ratio)
}

func blendColor(a, b color.RGBA, t float64) color.RGBA {
	clamp := func(v float64) uint8 {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(math.Round(v))
	}

	ai := float64(a.A)
	bi := float64(b.A)
	alpha := (1-t)*ai + t*bi
	if alpha == 0 {
		alpha = 1
	}

	return color.RGBA{
		R: clamp((1-t)*float64(a.R) + t*float64(b.R)),
		G: clamp((1-t)*float64(a.G) + t*float64(b.G)),
		B: clamp((1-t)*float64(a.B) + t*float64(b.B)),
		A: clamp(alpha),
	}
}

func seedFromString(seed string) int64 {
	if seed == "" {
		return time.Now().UnixNano()
	}
	h := int64(1469598103934665603)
	const prime = 1099511628211
	for i := 0; i < len(seed); i++ {
		h ^= int64(seed[i])
		h *= prime
	}
	return h
}

func clampInt(v, minVal, maxVal int) int {
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampFloat(v, minVal, maxVal float64) float64 {
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

func (req *mapRequest) normalize() (generationParams, error) {
	p := generationParams{
		width:      req.W,
		height:     req.H,
		tileString: req.Tiles,
		mode:       req.Mode,
		seed:       req.Seed,
	}

	if p.width <= 0 {
		if req.W == 0 {
			p.width = 100
		} else {
			return generationParams{}, fmt.Errorf("width must be positive")
		}
	}
	if p.height <= 0 {
		if req.H == 0 {
			p.height = 100
		} else {
			return generationParams{}, fmt.Errorf("height must be positive")
		}
	}

	if req.Ka != nil {
		p.ka = *req.Ka
	} else {
		p.ka = 1.0
	}

	if req.Cap != nil {
		p.cap = *req.Cap
		if p.cap < 0 {
			p.cap = 0
		}
	} else {
		p.cap = 0
	}

	if strings.TrimSpace(p.mode) == "" {
		p.mode = "merkez"
	}
	p.mode = strings.ToLower(p.mode)
	switch p.mode {
	case "merkez", "agirlik", "adalar", "iki-kita":
	default:
		return generationParams{}, fmt.Errorf("unsupported mode %q", p.mode)
	}

	if req.Rings != nil {
		p.rings = *req.Rings
	} else {
		p.rings = 10
	}
	if p.rings <= 0 {
		p.rings = 10
	}

	if req.RingStart != nil {
		p.ringStart = clampFloat(*req.RingStart, 0, 1)
	} else {
		p.ringStart = 0.1
	}
	if req.RingEnd != nil {
		p.ringEnd = clampFloat(*req.RingEnd, 0, 1)
	} else {
		p.ringEnd = 0.8
	}
	if p.ringEnd <= p.ringStart {
		adjustedEnd := clampFloat(p.ringStart+0.05, p.ringStart, 1)
		if adjustedEnd == p.ringStart {
			return generationParams{}, fmt.Errorf("ringEnd must be greater than ringStart")
		}
		p.ringEnd = adjustedEnd
	}

	if req.LogTone != nil {
		p.logTone = *req.LogTone != 0
	} else {
		p.logTone = true
	}

	if req.BrownCap != nil {
		p.brownCap = *req.BrownCap
	} else {
		p.brownCap = 8
	}
	if p.brownCap < 1 {
		p.brownCap = 1
	}

	if req.BgAlpha != nil {
		p.bgAlpha = *req.BgAlpha
	} else {
		p.bgAlpha = 0
	}

	if req.Islands != nil {
		p.islands = *req.Islands
	} else {
		p.islands = 4
	}

	if req.IslandRFrac != nil {
		p.islandRFrac = *req.IslandRFrac
	} else {
		p.islandRFrac = 0.25
	}
	if p.islandRFrac <= 0 {
		p.islandRFrac = 0.25
	}

	if req.Rotate != nil {
		p.rotate = *req.Rotate != 0
	} else {
		p.rotate = true
	}

	if req.N22 != nil {
		p.n22 = *req.N22
	}
	if req.N21 != nil {
		p.n21 = *req.N21
	}
	if req.N11 != nil {
		p.n11 = *req.N11
	}

	return p, nil
}

func generateMap(p generationParams) (generationResult, error) {
	specs, err := parseTileList(p.tileString)
	if err != nil {
		return generationResult{}, err
	}

	specs = applyLegacyTiles(specs, p.n22, p.n21, p.n11)
	activateMultiplier(specs, p.ka)
	batches := finalizeTileBatches(specs, p.cap)
	if len(batches) == 0 {
		return generationResult{}, fmt.Errorf("no tiles to place after cap adjustment")
	}

	seed := seedFromString(p.seed)
	rnd := rand.New(rand.NewSource(seed))
	gen := newGenerator(p.width, p.height, p.mode, p.rings, p.ringStart, p.ringEnd, p.islands, p.islandRFrac, rnd)

	img := image.NewRGBA(image.Rect(0, 0, p.width, p.height))
	bgAlphaClamped := clampInt(p.bgAlpha, 0, 255)
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{0, 0, 0, uint8(bgAlphaClamped)}}, image.Point{}, draw.Src)

	coverage := make([]int, p.width*p.height)
	totalPlacements := 0

	for _, batch := range batches {
		totalPlacements += batch.Count
		for i := 0; i < batch.Count; i++ {
			tw, th := batch.W, batch.H
			if p.rotate && tw != th && rnd.Intn(2) == 0 {
				tw, th = th, tw
			}
			if tw <= 0 || th <= 0 || tw > p.width || th > p.height {
				continue
			}
			x, y := gen.positionForTile(tw, th)
			gen.recordPlacement(x, y, tw, th)
			for yy := y; yy < y+th; yy++ {
				rowOffset := yy * p.width
				for xx := x; xx < x+tw; xx++ {
					idx := rowOffset + xx
					if idx >= 0 && idx < len(coverage) {
						coverage[idx]++
					}
				}
			}
		}
	}

	green := color.RGBA{R: 34, G: 139, B: 34, A: 255}
	brown := color.RGBA{R: 139, G: 69, B: 19, A: 255}

	for y := 0; y < p.height; y++ {
		for x := 0; x < p.width; x++ {
			idx := y*p.width + x
			c := coverage[idx]
			if c <= 0 {
				continue
			}
			col := coverageToColor(c, p.brownCap, p.logTone, green, brown)
			img.Set(x, y, col)
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return generationResult{}, fmt.Errorf("encode png: %w", err)
	}

	return generationResult{
		imageData:       buf.Bytes(),
		batches:         len(batches),
		totalPlacements: totalPlacements,
		seedValue:       seed,
	}, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func handleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST with JSON body"})
		return
	}

	defer r.Body.Close()

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var req mapRequest
	if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid JSON: %v", err)})
		return
	}

	params, err := req.normalize()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	start := time.Now()
	result, err := generateMap(params)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Tile-Batches", strconv.Itoa(result.batches))
	w.Header().Set("X-Tile-Count", strconv.Itoa(result.totalPlacements))
	w.Header().Set("X-Seed", strconv.FormatInt(result.seedValue, 10))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(result.imageData); err != nil {
		log.Printf("write response: %v", err)
	}

	log.Printf("generated %dx%d map mode=%s placements=%d batches=%d seed=%d duration=%s",
		params.width, params.height, params.mode, result.totalPlacements, result.batches, result.seedValue, time.Since(start))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "POST a JSON payload to /generate to receive a PNG map",
	})
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/generate", handleGenerate)
	mux.HandleFunc("/healthz", handleHealth)

	addr := "127.0.0.1:8080"
	log.Printf("map generator server listening on http://%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
