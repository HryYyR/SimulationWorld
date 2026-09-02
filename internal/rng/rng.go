package rng

const (
	StreamWorldGen uint64 = 1
	StreamWeather  uint64 = 2
	StreamBehavior uint64 = 3
	StreamLifespan uint64 = 4
	StreamOrder    uint64 = 5
	StreamRepro    uint64 = 6
)

type Rng struct {
	key uint64
	ctr uint64
}

func SplitMix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}

func Hash(seed uint64, parts ...uint64) uint64 {
	h := seed
	for _, p := range parts {
		h = SplitMix64(h ^ (p * 0xD6E8FEB86659FD93))
	}
	return h
}

func New(seed uint64, stream uint64, tick, id int) *Rng {
	return &Rng{key: Hash(seed, stream, uint64(tick), uint64(id))}
}

func (r *Rng) Uint64() uint64 {
	r.ctr++
	return SplitMix64(Hash(r.key, r.ctr))
}

func (r *Rng) Float64() float64 {
	return float64(r.Uint64()>>11) / (1 << 53)
}

func (r *Rng) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.Uint64() % uint64(n))
}

func (r *Rng) IntRange(min, max int) int {
	if max < min {
		min, max = max, min
	}
	return min + r.Intn(max-min+1)
}

func (r *Rng) Chance(p float64) bool {
	return r.Float64() < p
}
