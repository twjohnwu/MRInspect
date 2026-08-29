package lane

import (
	"sort"
	"strings"
)

// MergedFinding is a representative finding augmented with the lanes that
// reported its cluster. Merge replaces Severity with the cluster maximum and
// Citations with the contributions from all cluster members.
type MergedFinding struct {
	Finding
	ReportedBy    []string `json:"reportedBy"`
	CitationLanes []string `json:"citationLanes,omitempty"`
}

// Merge combines findings according to lane declaration order.
func Merge(laneOrder []string, results []LaneResult) []MergedFinding {
	lanePositions := buildLanePositions(laneOrder, results)
	groups := make(map[mergeKey]int)
	orderedGroups := make([][]mergeMember, 0)
	clusters := make([][]mergeMember, 0)
	encounter := 0

	for _, result := range results {
		for _, finding := range result.Findings {
			finding.File = normalizeMergeFile(finding.File)
			member := mergeMember{
				finding:      finding,
				laneID:       result.LaneID,
				lanePosition: lanePositions[result.LaneID],
				encounter:    encounter,
			}
			encounter++

			if finding.Line == nil {
				clusters = append(clusters, []mergeMember{member})
				continue
			}

			key := mergeKey{file: finding.File, category: finding.Category}
			groupIndex, exists := groups[key]
			if !exists {
				groupIndex = len(orderedGroups)
				groups[key] = groupIndex
				orderedGroups = append(orderedGroups, nil)
			}
			orderedGroups[groupIndex] = append(orderedGroups[groupIndex], member)
		}
	}

	for _, group := range orderedGroups {
		clusters = append(clusters, clusterByRepresentativeLine(group)...)
	}

	merged := make([]mergeOutput, 0, len(clusters))
	for _, cluster := range clusters {
		merged = append(merged, mergeCluster(cluster))
	}
	sort.SliceStable(merged, func(i, j int) bool {
		left, right := merged[i], merged[j]
		if rank := severityRank(left.finding.Severity) - severityRank(right.finding.Severity); rank != 0 {
			return rank < 0
		}
		if left.representativePosition != right.representativePosition {
			return left.representativePosition < right.representativePosition
		}
		if left.finding.File != right.finding.File {
			return left.finding.File < right.finding.File
		}
		leftLine, rightLine := mergeLine(left.finding.Line), mergeLine(right.finding.Line)
		if leftLine != rightLine {
			return leftLine < rightLine
		}
		if left.finding.Title != right.finding.Title {
			return left.finding.Title < right.finding.Title
		}
		return left.finding.Category < right.finding.Category
	})

	findings := make([]MergedFinding, len(merged))
	for i := range merged {
		findings[i] = merged[i].finding
	}
	return findings
}

type mergeKey struct {
	file     string
	category string
}

type mergeMember struct {
	finding      Finding
	laneID       string
	lanePosition int
	encounter    int
}

type mergeOutput struct {
	finding                MergedFinding
	representativePosition int
}

func buildLanePositions(laneOrder []string, results []LaneResult) map[string]int {
	positions := make(map[string]int, len(laneOrder)+len(results))
	for position, laneID := range laneOrder {
		if _, exists := positions[laneID]; !exists {
			positions[laneID] = position
		}
	}

	nextPosition := len(laneOrder)
	for _, result := range results {
		if _, exists := positions[result.LaneID]; exists {
			continue
		}
		positions[result.LaneID] = nextPosition
		nextPosition++
	}
	return positions
}

func normalizeMergeFile(file string) string {
	file = strings.TrimPrefix(file, "./")
	if strings.HasPrefix(file, "a/") || strings.HasPrefix(file, "b/") {
		file = file[2:]
	}
	return file
}

func clusterByRepresentativeLine(group []mergeMember) [][]mergeMember {
	sort.SliceStable(group, func(i, j int) bool {
		return *group[i].finding.Line < *group[j].finding.Line
	})

	clusters := make([][]mergeMember, 0)
	laneSeen := make([]map[string]struct{}, 0)
	for _, member := range group {
		if len(clusters) == 0 {
			clusters = append(clusters, []mergeMember{member})
			laneSeen = append(laneSeen, map[string]struct{}{member.laneID: {}})
			continue
		}

		current := clusters[len(clusters)-1]
		representativeLine := *current[0].finding.Line
		_, laneAlreadyInCluster := laneSeen[len(laneSeen)-1][member.laneID]
		if *member.finding.Line-representativeLine <= 3 && !laneAlreadyInCluster {
			clusters[len(clusters)-1] = append(current, member)
			laneSeen[len(laneSeen)-1][member.laneID] = struct{}{}
			continue
		}
		clusters = append(clusters, []mergeMember{member})
		laneSeen = append(laneSeen, map[string]struct{}{member.laneID: {}})
	}
	return clusters
}

func mergeCluster(cluster []mergeMember) mergeOutput {
	sort.SliceStable(cluster, func(i, j int) bool {
		if cluster[i].lanePosition != cluster[j].lanePosition {
			return cluster[i].lanePosition < cluster[j].lanePosition
		}
		return cluster[i].encounter < cluster[j].encounter
	})

	representative := cluster[0]
	finding := representative.finding
	finding.Severity = maximumSeverity(cluster)
	var citationLanes []string
	finding.Citations, citationLanes = mergeCitations(cluster)

	reportedBy := make([]string, 0, len(cluster))
	seenLanes := make(map[string]struct{}, len(cluster))
	for _, member := range cluster {
		if _, exists := seenLanes[member.laneID]; exists {
			continue
		}
		seenLanes[member.laneID] = struct{}{}
		reportedBy = append(reportedBy, member.laneID)
	}

	return mergeOutput{
		finding: MergedFinding{
			Finding:       finding,
			ReportedBy:    reportedBy,
			CitationLanes: citationLanes,
		},
		representativePosition: representative.lanePosition,
	}
}

func maximumSeverity(cluster []mergeMember) Severity {
	maximum := cluster[0].finding.Severity
	for _, member := range cluster[1:] {
		if severityRank(member.finding.Severity) < severityRank(maximum) {
			maximum = member.finding.Severity
		}
	}
	return maximum
}

func mergeCitations(cluster []mergeMember) ([]Citation, []string) {
	count := 0
	for _, member := range cluster {
		count += len(member.finding.Citations)
	}
	if count == 0 {
		return nil, nil
	}

	citations := make([]Citation, 0, count)
	citationLanes := make([]string, 0, count)
	for _, member := range cluster {
		citations = append(citations, member.finding.Citations...)
		for range member.finding.Citations {
			citationLanes = append(citationLanes, member.laneID)
		}
	}
	return citations, citationLanes
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityHigh:
		return 0
	case SeverityMedium:
		return 1
	case SeverityLow:
		return 2
	default:
		return 3
	}
}

func mergeLine(line *int) int {
	if line == nil {
		return -1
	}
	return *line
}
