package skill

import (
	"errors"
	"sort"
	"sync"

	"agent/internal/capability"
)

var ErrDuplicateVersion = errors.New("skill version already exists")

type Registry struct {
	mu       sync.RWMutex
	versions map[string]map[string]Manifest
	active   map[string]string
}

func NewRegistry() *Registry {
	return &Registry{versions: map[string]map[string]Manifest{}, active: map[string]string{}}
}

func (r *Registry) Add(m Manifest, caps *capability.Registry) error {
	if err := Validate(m, caps); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.versions[m.ID] == nil {
		r.versions[m.ID] = map[string]Manifest{}
	}
	if _, ok := r.versions[m.ID][m.Version]; ok {
		return ErrDuplicateVersion
	}
	r.versions[m.ID][m.Version] = Normalize(m)
	if m.Status == StatusActive {
		r.active[m.ID] = m.Version
	}
	return nil
}

func (r *Registry) Activate(id, version string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.versions[id][version]
	if !ok {
		return errors.New("skill version not found")
	}
	m.Status = StatusActive
	r.versions[id][version] = m
	r.active[id] = version
	return nil
}

func (r *Registry) Get(id string) (Manifest, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	version, ok := r.active[id]
	if !ok {
		return Manifest{}, false
	}
	m, ok := r.versions[id][version]
	return m, ok
}

func (r *Registry) List() []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []Manifest{}
	for id, version := range r.active {
		if m, ok := r.versions[id][version]; ok {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) Match(req MatchRequest) []Match {
	r.mu.RLock()
	defer r.mu.RUnlock()
	allowed := map[string]bool{}
	for _, id := range req.AllowedSkillIDs {
		allowed[id] = true
	}
	var out []Match
	for id, version := range r.active {
		if len(allowed) > 0 && !allowed[id] {
			continue
		}
		m := r.versions[id][version]
		if m.Status != StatusActive || !containsAll(m.Requires.Capabilities, req.RequiredCapabilities) {
			continue
		}
		if req.PrivacyClass == "confidential" && m.Privacy.MaxExternalTrust == "public_remote" {
			continue
		}
		score := 0.5 + float64(len(m.Requires.Capabilities))*0.1
		if req.Intent != "" && (containsWord(m.Name, req.Intent) || containsWord(m.Description, req.Intent)) {
			score += 0.5
		}
		out = append(out, Match{Manifest: m, Score: score, Reason: "active skill matches required capabilities and policy"})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].Manifest.ID < out[j].Manifest.ID
		}
		return out[i].Score > out[j].Score
	})
	return out
}

func containsAll(values, wanted []string) bool {
	set := map[string]bool{}
	for _, v := range values {
		set[v] = true
	}
	for _, v := range wanted {
		if !set[v] {
			return false
		}
	}
	return true
}
func containsWord(text, wanted string) bool {
	return wanted != "" && len(text) >= len(wanted) && (text == wanted || (len(text) > len(wanted) && (stringContains(text, wanted))))
}
func stringContains(text, wanted string) bool {
	for i := 0; i+len(wanted) <= len(text); i++ {
		if text[i:i+len(wanted)] == wanted {
			return true
		}
	}
	return false
}
