// Package readability extracts the main article content from an HTML
// document, discarding navigation, advertising and other boilerplate. It is a
// self-contained scoring algorithm over golang.org/x/net/html with no
// dependency on how the document was fetched.
package readability

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
)

var webFetchContentHints = []string{"article", "content", "entry", "main", "post", "story"}
var webFetchNegativeHints = []string{"advert", "banner", "breadcrumb", "comment", "consent", "cookie", "footer", "menu", "modal", "nav", "promo", "related", "share", "sidebar", "social", "subscribe"}
var webFetchReadabilityUnlikelyPattern = regexp.MustCompile(`-ad-|banner|breadcrumbs|combx|comment|community|disqus|extra|footer|gdpr|header|menu|pagination|pager|popup|related|remark|replies|rss|shoutbox|sidebar|skyscraper|social|sponsor|supplemental`)
var webFetchReadabilityMaybePattern = regexp.MustCompile(`and|article|body|column|content|main|shadow`)
var webFetchReadabilityPositivePattern = regexp.MustCompile(`article|body|content|entry|hentry|h-entry|main|page|post|text|blog|story`)
var webFetchReadabilityNegativePattern = regexp.MustCompile(`-ad-|hidden|banner|combx|comment|contact|footer|gdpr|masthead|media|meta|promo|related|scroll|share|shoutbox|sidebar|skyscraper|sponsor|shopping|tags|widget`)
var webFetchReadabilityCommaPattern = regexp.MustCompile(`[,\x{3001}\x{3002}\x{FF0C}]`)
var webFetchReadabilitySentencePattern = regexp.MustCompile(`\.( |$)`)
var webFetchReadabilityAdWordPattern = regexp.MustCompile(`^(ad|advertising|advertisement|promo)$`)
var webFetchReadabilityLoadingPattern = regexp.MustCompile(`^(loading|loading\.\.\.|loading…)$`)
var webFetchAlwaysDropTags = map[string]struct{}{
	"button":   {},
	"canvas":   {},
	"dialog":   {},
	"form":     {},
	"iframe":   {},
	"input":    {},
	"link":     {},
	"meta":     {},
	"noscript": {},
	"option":   {},
	"script":   {},
	"select":   {},
	"style":    {},
	"svg":      {},
	"template": {},
	"textarea": {},
}
var webFetchChromeTags = map[string]struct{}{
	"aside":  {},
	"footer": {},
	"nav":    {},
}
var webFetchReadabilityScorableTags = map[string]struct{}{
	"h2":      {},
	"h3":      {},
	"h4":      {},
	"h5":      {},
	"h6":      {},
	"p":       {},
	"pre":     {},
	"section": {},
	"td":      {},
}
var webFetchReadabilityContainerTags = map[string]struct{}{
	"div":     {},
	"header":  {},
	"h1":      {},
	"h2":      {},
	"h3":      {},
	"h4":      {},
	"h5":      {},
	"h6":      {},
	"section": {},
}
var webFetchReadabilityBlockTags = map[string]struct{}{
	"article":    {},
	"aside":      {},
	"blockquote": {},
	"div":        {},
	"dl":         {},
	"figure":     {},
	"footer":     {},
	"header":     {},
	"img":        {},
	"main":       {},
	"nav":        {},
	"ol":         {},
	"p":          {},
	"pre":        {},
	"section":    {},
	"table":      {},
	"ul":         {},
}
var webFetchReadabilityMediaTags = map[string]struct{}{
	"audio":  {},
	"embed":  {},
	"iframe": {},
	"img":    {},
	"object": {},
	"video":  {},
}
var webFetchReadabilityUnlikelyRoles = map[string]struct{}{
	"alert":         {},
	"alertdialog":   {},
	"complementary": {},
	"dialog":        {},
	"menu":          {},
	"menubar":       {},
	"navigation":    {},
}
var webFetchReadabilityHeadingTags = map[string]struct{}{
	"h1": {},
	"h2": {},
	"h3": {},
	"h4": {},
	"h5": {},
	"h6": {},
}
var webFetchReadabilityTextishTags = map[string]struct{}{
	"blockquote": {},
	"div":        {},
	"dl":         {},
	"img":        {},
	"li":         {},
	"ol":         {},
	"p":          {},
	"pre":        {},
	"span":       {},
	"table":      {},
	"td":         {},
	"ul":         {},
}

// ExtractHTMLForMarkdown returns the subtree of body most likely to hold the
// article text, serialized as HTML. The original document is returned
// unchanged when no better candidate is found.
func ExtractHTMLForMarkdown(body string) string {
	document, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return body
	}

	root := findFirstWebFetchElement(document, "body")
	if root == nil {
		root = document
	}
	selected := selectWebFetchContentRoot(root)
	pruneWebFetchChrome(selected)
	prepareWebFetchArticle(selected)

	var builder bytes.Buffer
	if err := xhtml.Render(&builder, selected); err != nil {
		return body
	}
	return builder.String()
}

func findFirstWebFetchElement(root *xhtml.Node, tagName string) *xhtml.Node {
	if root == nil {
		return nil
	}
	if root.Type == xhtml.ElementNode && strings.EqualFold(root.Data, tagName) {
		return root
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := findFirstWebFetchElement(child, tagName); found != nil {
			return found
		}
	}
	return nil
}

func selectWebFetchContentRoot(root *xhtml.Node) *xhtml.Node {
	if candidate := extractWebFetchReadableContent(root); candidate != nil {
		return candidate
	}
	return selectWebFetchHeuristicContentRoot(root)
}

func selectWebFetchHeuristicContentRoot(root *xhtml.Node) *xhtml.Node {
	if root == nil {
		return nil
	}
	bodyText, _ := webFetchTextMetrics(root, false)
	if bodyText == 0 {
		return root
	}

	bestNode := root
	bestScore := bodyText
	for _, candidate := range collectWebFetchContentCandidates(root) {
		visibleText, linkText := webFetchTextMetrics(candidate, false)
		if visibleText < 200 {
			continue
		}
		if visibleText*100 < bodyText*35 {
			continue
		}
		score := visibleText - linkText
		if score > bestScore {
			bestNode = candidate
			bestScore = score
		}
	}
	return bestNode
}

func extractWebFetchReadableContent(root *xhtml.Node) *xhtml.Node {
	if root == nil {
		return nil
	}

	elementsToScore := collectWebFetchReadabilityNodes(root)
	if len(elementsToScore) == 0 {
		return nil
	}

	baseScores := make(map[*xhtml.Node]float64)
	adjustedScores := make(map[*xhtml.Node]float64)
	candidates := make([]*xhtml.Node, 0, len(elementsToScore))

	for _, node := range elementsToScore {
		innerText := webFetchInnerText(node, true)
		if len(innerText) < 25 {
			continue
		}

		ancestors := webFetchNodeAncestors(node, 5)
		if len(ancestors) == 0 {
			continue
		}

		contentScore := 1.0
		contentScore += float64(len(webFetchReadabilityCommaPattern.FindAllStringIndex(innerText, -1)) + 1)
		contentScore += float64(min(len(innerText)/100, 3))

		for depth, ancestor := range ancestors {
			if ancestor == nil || ancestor.Type != xhtml.ElementNode {
				continue
			}
			if _, ok := baseScores[ancestor]; !ok {
				baseScores[ancestor] = webFetchInitializeReadabilityScore(ancestor)
				candidates = append(candidates, ancestor)
			}

			divider := 1.0
			switch depth {
			case 0:
				divider = 1
			case 1:
				divider = 2
			default:
				divider = float64(depth * 3)
			}
			baseScores[ancestor] += contentScore / divider
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	topCandidates := make([]*xhtml.Node, 0, 5)
	for _, candidate := range candidates {
		score := baseScores[candidate] * (1 - webFetchLinkDensity(candidate))
		adjustedScores[candidate] = score
		topCandidates = insertWebFetchTopCandidate(topCandidates, candidate, adjustedScores, 5)
	}

	if len(topCandidates) == 0 {
		return nil
	}

	topCandidate := refineWebFetchTopCandidate(topCandidates[0], adjustedScores)
	if topCandidate == nil || adjustedScores[topCandidate] <= 0 {
		return nil
	}

	article := collectWebFetchReadableSiblings(topCandidate, adjustedScores)
	if article == nil {
		return topCandidate
	}
	if len(webFetchInnerText(article, true)) < 140 {
		return topCandidate
	}
	return article
}

func collectWebFetchReadabilityNodes(root *xhtml.Node) []*xhtml.Node {
	if root == nil {
		return nil
	}

	var nodes []*xhtml.Node
	var walk func(node *xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil {
			return
		}
		if node.Type == xhtml.ElementNode {
			if !webFetchIsProbablyVisible(node) || webFetchShouldSkipReadabilityNode(node) {
				return
			}
			tagName := webFetchElementName(node)
			if _, ok := webFetchReadabilityContainerTags[tagName]; ok && webFetchIsElementWithoutContent(node) {
				return
			}
			if _, ok := webFetchReadabilityScorableTags[tagName]; ok {
				nodes = append(nodes, node)
			}
			if tagName == "div" && !webFetchHasChildBlockElement(node) {
				nodes = append(nodes, node)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return nodes
}

func insertWebFetchTopCandidate(topCandidates []*xhtml.Node, candidate *xhtml.Node, scores map[*xhtml.Node]float64, limit int) []*xhtml.Node {
	inserted := false
	for index, existing := range topCandidates {
		if scores[candidate] > scores[existing] {
			topCandidates = append(topCandidates, nil)
			copy(topCandidates[index+1:], topCandidates[index:])
			topCandidates[index] = candidate
			inserted = true
			break
		}
	}
	if !inserted && len(topCandidates) < limit {
		topCandidates = append(topCandidates, candidate)
	}
	if len(topCandidates) > limit {
		topCandidates = topCandidates[:limit]
	}
	return topCandidates
}

func refineWebFetchTopCandidate(topCandidate *xhtml.Node, scores map[*xhtml.Node]float64) *xhtml.Node {
	if topCandidate == nil {
		return nil
	}

	lastScore := scores[topCandidate]
	scoreFloor := lastScore / 3
	for parent := topCandidate.Parent; parent != nil && webFetchElementName(parent) != "body"; parent = parent.Parent {
		parentScore, ok := scores[parent]
		if !ok {
			continue
		}
		if parentScore < scoreFloor {
			break
		}
		if parentScore > lastScore {
			topCandidate = parent
			break
		}
		lastScore = parentScore
	}

	for parent := topCandidate.Parent; parent != nil && webFetchElementName(parent) != "body" && webFetchElementChildCount(parent) == 1; parent = topCandidate.Parent {
		topCandidate = parent
	}
	return topCandidate
}

func collectWebFetchReadableSiblings(topCandidate *xhtml.Node, scores map[*xhtml.Node]float64) *xhtml.Node {
	if topCandidate == nil || topCandidate.Parent == nil {
		return nil
	}

	parent := topCandidate.Parent
	article := &xhtml.Node{Type: xhtml.ElementNode, Data: "div"}
	threshold := scores[topCandidate] * 0.2
	if threshold < 10 {
		threshold = 10
	}
	topClass := strings.TrimSpace(webFetchAttr(topCandidate, "class"))

	for sibling := parent.FirstChild; sibling != nil; {
		nextSibling := sibling.NextSibling
		shouldAppend := sibling == topCandidate

		if !shouldAppend && sibling.Type == xhtml.ElementNode {
			bonus := 0.0
			if topClass != "" && strings.TrimSpace(webFetchAttr(sibling, "class")) == topClass {
				bonus = scores[topCandidate] * 0.2
			}
			if scores[sibling]+bonus >= threshold {
				shouldAppend = true
			} else if webFetchElementName(sibling) == "p" {
				nodeContent := webFetchInnerText(sibling, true)
				nodeLength := len(nodeContent)
				linkDensity := webFetchLinkDensity(sibling)
				if nodeLength > 80 && linkDensity < 0.25 {
					shouldAppend = true
				} else if nodeLength > 0 && nodeLength < 80 && linkDensity == 0 && webFetchReadabilitySentencePattern.MatchString(nodeContent) {
					shouldAppend = true
				}
			}
		}

		if shouldAppend {
			parent.RemoveChild(sibling)
			article.AppendChild(sibling)
		}
		sibling = nextSibling
	}

	if article.FirstChild == nil {
		return nil
	}
	return article
}

func webFetchInitializeReadabilityScore(node *xhtml.Node) float64 {
	score := 0.0
	switch webFetchElementName(node) {
	case "div":
		score += 5
	case "blockquote", "pre", "td":
		score += 3
	case "address", "dd", "dl", "dt", "form", "li", "ol", "ul":
		score -= 3
	case "h1", "h2", "h3", "h4", "h5", "h6", "th":
		score -= 5
	}
	return score + webFetchClassWeight(node)
}

func webFetchClassWeight(node *xhtml.Node) float64 {
	marker := strings.TrimSpace(webFetchAttr(node, "class"))
	id := strings.TrimSpace(webFetchAttr(node, "id"))
	weight := 0.0
	if marker != "" {
		if webFetchReadabilityNegativePattern.MatchString(marker) {
			weight -= 25
		}
		if webFetchReadabilityPositivePattern.MatchString(marker) {
			weight += 25
		}
	}
	if id != "" {
		if webFetchReadabilityNegativePattern.MatchString(id) {
			weight -= 25
		}
		if webFetchReadabilityPositivePattern.MatchString(id) {
			weight += 25
		}
	}
	return weight
}

func webFetchLinkDensity(node *xhtml.Node) float64 {
	textLength := len(webFetchInnerText(node, true))
	if textLength == 0 {
		return 0
	}

	linkText := 0.0
	var walk func(current *xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current == nil {
			return
		}
		if current.Type == xhtml.ElementNode && webFetchElementName(current) == "a" {
			coefficient := 1.0
			href := strings.TrimSpace(webFetchAttr(current, "href"))
			if strings.HasPrefix(href, "#") {
				coefficient = 0.3
			}
			linkText += float64(len(webFetchInnerText(current, true))) * coefficient
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return linkText / float64(textLength)
}

func webFetchNodeAncestors(node *xhtml.Node, maxDepth int) []*xhtml.Node {
	ancestors := make([]*xhtml.Node, 0, maxDepth)
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if parent.Type == xhtml.ElementNode {
			ancestors = append(ancestors, parent)
			if maxDepth > 0 && len(ancestors) == maxDepth {
				break
			}
		}
	}
	return ancestors
}

func webFetchInnerText(node *xhtml.Node, normalizeSpaces bool) string {
	if node == nil {
		return ""
	}

	var builder strings.Builder
	var walk func(current *xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current == nil {
			return
		}
		switch current.Type {
		case xhtml.TextNode:
			builder.WriteString(current.Data)
			builder.WriteByte(' ')
		case xhtml.ElementNode:
			if _, ok := webFetchAlwaysDropTags[webFetchElementName(current)]; ok {
				return
			}
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
			if _, ok := webFetchReadabilityBlockTags[webFetchElementName(current)]; ok {
				builder.WriteByte(' ')
			}
		}
	}
	walk(node)
	text := strings.TrimSpace(builder.String())
	if !normalizeSpaces {
		return text
	}
	return strings.Join(strings.Fields(text), " ")
}

func webFetchIsProbablyVisible(node *xhtml.Node) bool {
	if node == nil {
		return false
	}
	if _, hidden := webFetchNodeAttr(node, "hidden"); hidden {
		return false
	}
	if ariaHidden, ok := webFetchNodeAttr(node, "aria-hidden"); ok && strings.EqualFold(strings.TrimSpace(ariaHidden), "true") && !strings.Contains(strings.ToLower(webFetchAttr(node, "class")), "fallback-image") {
		return false
	}
	if style, ok := webFetchNodeAttr(node, "style"); ok {
		style = strings.ToLower(style)
		if strings.Contains(style, "display:none") || strings.Contains(style, "display: none") || strings.Contains(style, "visibility:hidden") || strings.Contains(style, "visibility: hidden") {
			return false
		}
	}
	return true
}

func webFetchShouldSkipReadabilityNode(node *xhtml.Node) bool {
	if node == nil || node.Type != xhtml.ElementNode {
		return false
	}
	role := strings.ToLower(strings.TrimSpace(webFetchAttr(node, "role")))
	if _, ok := webFetchReadabilityUnlikelyRoles[role]; ok {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(webFetchAttr(node, "aria-modal")), "true") && role == "dialog" {
		return true
	}

	matchString := strings.ToLower(strings.TrimSpace(webFetchAttr(node, "class") + " " + webFetchAttr(node, "id")))
	tagName := webFetchElementName(node)
	if webFetchReadabilityUnlikelyPattern.MatchString(matchString) &&
		!webFetchReadabilityMaybePattern.MatchString(matchString) &&
		!webFetchHasAncestorTag(node, "table") &&
		!webFetchHasAncestorTag(node, "code") &&
		tagName != "body" &&
		tagName != "a" {
		return true
	}
	return false
}

func webFetchHasAncestorTag(node *xhtml.Node, tagName string) bool {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if webFetchElementName(parent) == tagName {
			return true
		}
	}
	return false
}

func webFetchIsElementWithoutContent(node *xhtml.Node) bool {
	if node == nil {
		return true
	}
	if strings.TrimSpace(webFetchInnerText(node, true)) != "" {
		return false
	}
	return !webFetchHasDescendantTag(node, webFetchReadabilityMediaTags)
}

func webFetchHasDescendantTag(node *xhtml.Node, tagSet map[string]struct{}) bool {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode {
			if _, ok := tagSet[webFetchElementName(child)]; ok {
				return true
			}
		}
		if webFetchHasDescendantTag(child, tagSet) {
			return true
		}
	}
	return false
}

func webFetchHasChildBlockElement(node *xhtml.Node) bool {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != xhtml.ElementNode {
			continue
		}
		if _, ok := webFetchReadabilityBlockTags[webFetchElementName(child)]; ok {
			return true
		}
	}
	return false
}

func webFetchElementChildCount(node *xhtml.Node) int {
	count := 0
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode {
			count++
		}
	}
	return count
}

func webFetchElementName(node *xhtml.Node) string {
	if node == nil || node.Type != xhtml.ElementNode {
		return ""
	}
	return strings.ToLower(node.Data)
}

func webFetchNodeAttr(node *xhtml.Node, key string) (string, bool) {
	if node == nil {
		return "", false
	}
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, key) {
			return attribute.Val, true
		}
	}
	return "", false
}

func collectWebFetchContentCandidates(root *xhtml.Node) []*xhtml.Node {
	if root == nil {
		return nil
	}
	var candidates []*xhtml.Node
	var visit func(node *xhtml.Node)
	visit = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && isWebFetchContentCandidate(node) {
			candidates = append(candidates, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return candidates
}

func isWebFetchContentCandidate(node *xhtml.Node) bool {
	if node == nil || node.Type != xhtml.ElementNode {
		return false
	}
	if node.Data == "main" || node.Data == "article" {
		return true
	}
	role := strings.ToLower(strings.TrimSpace(webFetchAttr(node, "role")))
	if role == "main" || role == "article" {
		return true
	}
	marker := strings.ToLower(webFetchAttr(node, "id") + " " + webFetchAttr(node, "class"))
	if marker == "" || containsAnyWebFetchHint(marker, webFetchNegativeHints) {
		return false
	}
	return containsAnyWebFetchHint(marker, webFetchContentHints)
}

func pruneWebFetchChrome(root *xhtml.Node) {
	if root == nil {
		return
	}
	for child := root.FirstChild; child != nil; {
		nextChild := child.NextSibling
		if shouldPruneWebFetchNode(child) {
			root.RemoveChild(child)
			child = nextChild
			continue
		}
		pruneWebFetchChrome(child)
		child = nextChild
	}
}

func shouldPruneWebFetchNode(node *xhtml.Node) bool {
	if node == nil || node.Type != xhtml.ElementNode {
		return false
	}
	tagName := strings.ToLower(node.Data)
	if _, ok := webFetchAlwaysDropTags[tagName]; ok {
		return true
	}
	if _, ok := webFetchChromeTags[tagName]; ok {
		return true
	}
	if nodeHasHiddenWebFetchAttrs(node) {
		return true
	}
	marker := strings.ToLower(webFetchAttr(node, "id") + " " + webFetchAttr(node, "class") + " " + webFetchAttr(node, "role") + " " + webFetchAttr(node, "aria-label"))
	return containsAnyWebFetchHint(marker, webFetchNegativeHints)
}

func prepareWebFetchArticle(root *xhtml.Node) {
	if root == nil {
		return
	}
	for _, tagName := range []string{"form", "fieldset"} {
		webFetchCleanConditionally(root, tagName)
	}
	for _, tagName := range []string{"object", "embed", "footer", "link", "aside", "iframe", "input", "textarea", "select", "button"} {
		webFetchRemoveTag(root, tagName)
	}
	webFetchCleanHeaders(root)
	for _, tagName := range []string{"table", "ul", "div"} {
		webFetchCleanConditionally(root, tagName)
	}
	webFetchRenameTag(root, "h1", "h2")
	webFetchRemoveEmptyParagraphs(root)
	webFetchCollapseSingleCellTables(root)
}

func webFetchRemoveTag(root *xhtml.Node, tagName string) {
	for _, node := range webFetchCollectNodesByTag(root, tagName) {
		if node.Parent != nil {
			node.Parent.RemoveChild(node)
		}
	}
}

func webFetchCleanHeaders(root *xhtml.Node) {
	for _, node := range webFetchCollectNodesByTag(root, "h1", "h2") {
		if node.Parent != nil && webFetchClassWeight(node) < 0 {
			node.Parent.RemoveChild(node)
		}
	}
}

func webFetchCleanConditionally(root *xhtml.Node, tagName string) {
	for _, node := range webFetchCollectNodesByTag(root, tagName) {
		if node == nil || node.Parent == nil {
			continue
		}
		if tagName == "table" && webFetchIsDataTable(node) {
			continue
		}
		if webFetchHasAncestorMatching(node, "table", webFetchIsDataTable) || webFetchHasAncestorTag(node, "code") || webFetchSubtreeHasDataTable(node) {
			continue
		}

		weight := webFetchClassWeight(node)
		if weight < 0 {
			node.Parent.RemoveChild(node)
			continue
		}
		if webFetchCharCount(node, ",") >= 10 {
			continue
		}

		innerText := webFetchInnerText(node, true)
		if innerText == "" {
			node.Parent.RemoveChild(node)
			continue
		}
		normalizedText := strings.ToLower(strings.TrimSpace(innerText))
		if webFetchReadabilityAdWordPattern.MatchString(normalizedText) || webFetchReadabilityLoadingPattern.MatchString(normalizedText) {
			node.Parent.RemoveChild(node)
			continue
		}

		paragraphCount := len(webFetchCollectNodesByTag(node, "p"))
		imageCount := len(webFetchCollectNodesByTag(node, "img"))
		listItemCount := len(webFetchCollectNodesByTag(node, "li")) - 100
		inputCount := len(webFetchCollectNodesByTag(node, "input"))
		embedCount := len(webFetchCollectNodesByTag(node, "object", "embed", "iframe"))
		contentLength := len(innerText)
		linkDensity := webFetchLinkDensity(node)
		headingDensity := webFetchTextDensity(node, webFetchReadabilityHeadingTags)
		textDensity := webFetchTextDensity(node, webFetchReadabilityTextishTags)
		isList := tagName == "ul" || tagName == "ol" || webFetchListDensity(node) > 0.9
		isFigureChild := webFetchHasAncestorTag(node, "figure")

		shouldRemove := false
		if !isFigureChild && imageCount > 1 && float64(paragraphCount) < float64(imageCount)/2 {
			shouldRemove = true
		}
		if !isList && listItemCount > paragraphCount {
			shouldRemove = true
		}
		if inputCount > paragraphCount/3 {
			shouldRemove = true
		}
		if !isList && !isFigureChild && headingDensity < 0.9 && contentLength < 25 && (imageCount == 0 || imageCount > 2) && linkDensity > 0 {
			shouldRemove = true
		}
		if !isList && weight < 25 && linkDensity > 0.2 {
			shouldRemove = true
		}
		if weight >= 25 && linkDensity > 0.5 {
			shouldRemove = true
		}
		if (embedCount == 1 && contentLength < 75) || embedCount > 1 {
			shouldRemove = true
		}
		if imageCount == 0 && textDensity == 0 {
			shouldRemove = true
		}

		if shouldRemove && !(isList && webFetchLooksLikeImageList(node, imageCount)) {
			node.Parent.RemoveChild(node)
		}
	}
}

func webFetchTextDensity(node *xhtml.Node, tagSet map[string]struct{}) float64 {
	textLength := len(webFetchInnerText(node, true))
	if textLength == 0 {
		return 0
	}
	childTextLength := 0
	for _, child := range webFetchCollectNodes(node, func(candidate *xhtml.Node) bool {
		_, ok := tagSet[webFetchElementName(candidate)]
		return ok
	}) {
		childTextLength += len(webFetchInnerText(child, true))
	}
	return float64(childTextLength) / float64(textLength)
}

func webFetchListDensity(node *xhtml.Node) float64 {
	textLength := len(webFetchInnerText(node, true))
	if textLength == 0 {
		return 0
	}
	listLength := 0
	for _, list := range webFetchCollectNodesByTag(node, "ul", "ol") {
		listLength += len(webFetchInnerText(list, true))
	}
	return float64(listLength) / float64(textLength)
}

func webFetchLooksLikeImageList(node *xhtml.Node, imageCount int) bool {
	children := webFetchElementChildren(node)
	if len(children) == 0 {
		return false
	}
	for _, child := range children {
		if len(webFetchElementChildren(child)) > 1 {
			return false
		}
	}
	return imageCount == len(webFetchCollectNodesByTag(node, "li"))
}

func webFetchCharCount(node *xhtml.Node, target string) int {
	return strings.Count(webFetchInnerText(node, true), target)
}

func webFetchHasAncestorMatching(node *xhtml.Node, tagName string, predicate func(*xhtml.Node) bool) bool {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if webFetchElementName(parent) == tagName && predicate(parent) {
			return true
		}
	}
	return false
}

func webFetchSubtreeHasDataTable(node *xhtml.Node) bool {
	for _, table := range webFetchCollectNodesByTag(node, "table") {
		if table != node && webFetchIsDataTable(table) {
			return true
		}
	}
	return false
}

func webFetchIsDataTable(node *xhtml.Node) bool {
	if webFetchElementName(node) != "table" {
		return false
	}
	if role, ok := webFetchNodeAttr(node, "role"); ok && strings.EqualFold(strings.TrimSpace(role), "presentation") {
		return false
	}
	if datatable, ok := webFetchNodeAttr(node, "datatable"); ok && strings.TrimSpace(datatable) == "0" {
		return false
	}
	if summary, ok := webFetchNodeAttr(node, "summary"); ok && strings.TrimSpace(summary) != "" {
		return true
	}
	for _, caption := range webFetchCollectNodesByTag(node, "caption") {
		if strings.TrimSpace(webFetchInnerText(caption, true)) != "" {
			return true
		}
	}
	for _, tagName := range []string{"col", "colgroup", "tfoot", "thead", "th"} {
		if len(webFetchCollectNodesByTag(node, tagName)) > 0 {
			return true
		}
	}
	for _, table := range webFetchCollectNodesByTag(node, "table") {
		if table != node {
			return false
		}
	}
	rows, columns := webFetchTableDimensions(node)
	if rows == 1 || columns == 1 {
		return false
	}
	if rows >= 10 || columns > 4 {
		return true
	}
	return rows*columns > 10
}

func webFetchTableDimensions(table *xhtml.Node) (int, int) {
	rows := 0
	maxColumns := 0
	for _, row := range webFetchCollectNodesByTag(table, "tr") {
		rows++
		columnsInRow := 0
		for _, cell := range webFetchElementChildren(row) {
			name := webFetchElementName(cell)
			if name != "td" && name != "th" {
				continue
			}
			colspan := 1
			if rawColspan, ok := webFetchNodeAttr(cell, "colspan"); ok {
				var parsed int
				_, err := fmt.Sscanf(strings.TrimSpace(rawColspan), "%d", &parsed)
				if err == nil && parsed > 1 {
					colspan = parsed
				}
			}
			columnsInRow += colspan
		}
		if columnsInRow > maxColumns {
			maxColumns = columnsInRow
		}
	}
	return rows, maxColumns
}

func webFetchRenameTag(root *xhtml.Node, fromTag string, toTag string) {
	for _, node := range webFetchCollectNodesByTag(root, fromTag) {
		node.Data = toTag
	}
}

func webFetchRemoveEmptyParagraphs(root *xhtml.Node) {
	for _, paragraph := range webFetchCollectNodesByTag(root, "p") {
		if paragraph.Parent == nil {
			continue
		}
		contentElementCount := len(webFetchCollectNodesByTag(paragraph, "img", "embed", "object", "iframe"))
		if contentElementCount == 0 && webFetchInnerText(paragraph, false) == "" {
			paragraph.Parent.RemoveChild(paragraph)
		}
	}
}

func webFetchCollapseSingleCellTables(root *xhtml.Node) {
	for _, table := range webFetchCollectNodesByTag(root, "table") {
		if table.Parent == nil || webFetchIsDataTable(table) {
			continue
		}
		body := table
		if webFetchHasSingleTagInsideElement(table, "tbody") {
			body = webFetchFirstElementChild(table)
		}
		if body == nil || !webFetchHasSingleTagInsideElement(body, "tr") {
			continue
		}
		row := webFetchFirstElementChild(body)
		if row == nil || !webFetchHasSingleTagInsideElement(row, "td") {
			continue
		}
		cell := webFetchFirstElementChild(row)
		replacementTag := "div"
		if !webFetchHasChildBlockElement(cell) {
			replacementTag = "p"
		}
		replacement := &xhtml.Node{Type: xhtml.ElementNode, Data: replacementTag}
		for cell.FirstChild != nil {
			replacement.AppendChild(cell.FirstChild)
		}
		table.Parent.InsertBefore(replacement, table)
		table.Parent.RemoveChild(table)
	}
}

func webFetchHasSingleTagInsideElement(node *xhtml.Node, tagName string) bool {
	children := webFetchElementChildren(node)
	if len(children) != 1 || webFetchElementName(children[0]) != tagName {
		return false
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.TextNode && strings.TrimSpace(child.Data) != "" {
			return false
		}
	}
	return true
}

func webFetchFirstElementChild(node *xhtml.Node) *xhtml.Node {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode {
			return child
		}
	}
	return nil
}

func webFetchElementChildren(node *xhtml.Node) []*xhtml.Node {
	children := make([]*xhtml.Node, 0, webFetchElementChildCount(node))
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode {
			children = append(children, child)
		}
	}
	return children
}

func webFetchCollectNodesByTag(root *xhtml.Node, tagNames ...string) []*xhtml.Node {
	tagSet := make(map[string]struct{}, len(tagNames))
	for _, tagName := range tagNames {
		tagSet[strings.ToLower(tagName)] = struct{}{}
	}
	return webFetchCollectNodes(root, func(node *xhtml.Node) bool {
		_, ok := tagSet[webFetchElementName(node)]
		return ok
	})
}

func webFetchCollectNodes(root *xhtml.Node, predicate func(*xhtml.Node) bool) []*xhtml.Node {
	if root == nil {
		return nil
	}
	nodes := make([]*xhtml.Node, 0)
	var walk func(node *xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil {
			return
		}
		if node.Type == xhtml.ElementNode && predicate(node) {
			nodes = append(nodes, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return nodes
}

func nodeHasHiddenWebFetchAttrs(node *xhtml.Node) bool {
	if node == nil {
		return false
	}
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, "hidden") {
			return true
		}
		if strings.EqualFold(attribute.Key, "aria-hidden") && strings.EqualFold(strings.TrimSpace(attribute.Val), "true") {
			return true
		}
	}
	return false
}

func webFetchAttr(node *xhtml.Node, key string) string {
	if node == nil {
		return ""
	}
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, key) {
			return attribute.Val
		}
	}
	return ""
}

func containsAnyWebFetchHint(text string, hints []string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, hint := range hints {
		if strings.Contains(text, hint) {
			return true
		}
	}
	return false
}

func webFetchTextMetrics(node *xhtml.Node, insideLink bool) (int, int) {
	if node == nil {
		return 0, 0
	}
	switch node.Type {
	case xhtml.TextNode:
		textLength := len(strings.Join(strings.Fields(node.Data), " "))
		if insideLink {
			return textLength, textLength
		}
		return textLength, 0
	case xhtml.ElementNode:
		if _, ok := webFetchAlwaysDropTags[strings.ToLower(node.Data)]; ok {
			return 0, 0
		}
	}

	childInsideLink := insideLink || (node.Type == xhtml.ElementNode && node.Data == "a")
	visibleText := 0
	linkText := 0
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		childVisibleText, childLinkText := webFetchTextMetrics(child, childInsideLink)
		visibleText += childVisibleText
		linkText += childLinkText
	}
	return visibleText, linkText
}
