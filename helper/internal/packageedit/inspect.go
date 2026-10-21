package packageedit

// BrewDeclaration is a validated, static Brewfile entry. Line preserves options
// when another workflow builds an additive candidate or an install-only file.
type BrewDeclaration struct {
	Kind, Name, Line string
}

func InspectBrew(source string) ([]BrewDeclaration, error) {
	entries, err := brewEntries(source)
	if err != nil {
		return nil, err
	}
	result := make([]BrewDeclaration, 0, len(entries))
	parts := lines(source)
	for _, entry := range entries {
		result = append(result, BrewDeclaration{entry.Kind, entry.Name, parts[entry.Index]})
	}
	return result, nil
}
