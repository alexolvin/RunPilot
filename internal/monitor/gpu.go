package monitor

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"runpilot/internal/proto"
)

// ParseNvidia — вывод
// nvidia-smi --query-gpu=index,utilization.gpu,temperature.gpu,power.draw
// --format=csv,noheader,nounits.
// Строка на карту: «index, util %, temp °C, power W».
func ParseNvidia(out []byte) ([]proto.GPUCard, error) {
	var cards []proto.GPUCard
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Split(line, ",")
		if len(f) != nvidiaFieldCount {
			return nil, fmt.Errorf("monitor: nvidia: ждём 6 полей, получено %d", len(f))
		}
		card := proto.GPUCard{}
		var err error
		if card.Index, err = strconv.Atoi(strings.TrimSpace(f[0])); err != nil {
			return nil, fmt.Errorf("monitor: nvidia: index: %w", err)
		}
		if card.UtilPercent, err = strconv.Atoi(strings.TrimSpace(f[1])); err != nil {
			return nil, fmt.Errorf("monitor: nvidia: utilization: %w", err)
		}
		if card.TempC, err = strconv.Atoi(strings.TrimSpace(f[gpuFieldTemp])); err != nil {
			return nil, fmt.Errorf("monitor: nvidia: temperature: %w", err)
		}
		power, err := strconv.ParseFloat(strings.TrimSpace(f[gpuFieldPower]), floatBitSize)
		if err != nil {
			return nil, fmt.Errorf("monitor: nvidia: power.draw: %w", err)
		}
		card.PowerW = int(power)
		cards = append(cards, card)
	}
	if len(cards) == 0 {
		return nil, fmt.Errorf("monitor: nvidia: нет карт в выводе")
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Index < cards[j].Index })
	return cards, nil
}

// ParseROCm — вывод
// rocm-smi --showuse --showtemp --showpower --json:
// {"cardN": {"GPU use (%)": …, "GPU temp (degC)": …, "GPU power (watts)": …}}.
// Значения — число или строка (зависит от версии rocm-smi).
func ParseROCm(out []byte) ([]proto.GPUCard, error) {
	var raw map[string]map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("monitor: rocm: не JSON: %w", err)
	}
	var cards []proto.GPUCard
	for key, fields := range raw {
		card := proto.GPUCard{}
		idx, err := numAny(strings.TrimPrefix(key, "card"))
		if err != nil {
			return nil, fmt.Errorf("monitor: rocm: %s: индекс карты: %w", key, err)
		}
		card.Index = int(idx)
		if v, err := numAny(fields["GPU use (%)"]); err != nil {
			return nil, fmt.Errorf("monitor: rocm: %s: GPU use: %w", key, err)
		} else {
			card.UtilPercent = int(v)
		}
		if v, err := numAny(fields["GPU temp (degC)"]); err != nil {
			return nil, fmt.Errorf("monitor: rocm: %s: GPU temp: %w", key, err)
		} else {
			card.TempC = int(v)
		}
		if v, err := numAny(fields["GPU power (watts)"]); err != nil {
			return nil, fmt.Errorf("monitor: rocm: %s: GPU power: %w", key, err)
		} else {
			card.PowerW = int(v)
		}
		cards = append(cards, card)
	}
	if len(cards) == 0 {
		return nil, fmt.Errorf("monitor: rocm: нет карт в выводе")
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Index < cards[j].Index })
	return cards, nil
}

// numAny — JSON-значение (число или строка) → float64.
func numAny(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	case string:
		return strconv.ParseFloat(n, floatBitSize)
	case nil:
		return 0, fmt.Errorf("нет значения")
	default:
		return 0, fmt.Errorf("ожидается число, получено %T", v)
	}
}
