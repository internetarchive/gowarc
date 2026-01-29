package warc

import (
	"sync"
	"testing"
)

// TestLocalCounter tests the localCounter implementation
func TestLocalCounter(t *testing.T) {
	c := &localCounter{}

	// Test initial value
	if c.Get() != 0 {
		t.Errorf("Expected initial value 0, got %d", c.Get())
	}

	// Test Inc
	c.Inc()
	if c.Get() != 1 {
		t.Errorf("Expected value 1 after Inc, got %d", c.Get())
	}

	// Test Add
	c.Add(5)
	if c.Get() != 6 {
		t.Errorf("Expected value 6 after Add(5), got %d", c.Get())
	}

	// Test Add with negative value (counters should still accept it)
	c.Add(-2)
	if c.Get() != 4 {
		t.Errorf("Expected value 4 after Add(-2), got %d", c.Get())
	}
}

// TestLocalGauge tests the localGauge implementation
func TestLocalGauge(t *testing.T) {
	g := &localGauge{}

	// Test initial value
	if g.Get() != 0 {
		t.Errorf("Expected initial value 0, got %d", g.Get())
	}

	// Test Set
	g.Set(10)
	if g.Get() != 10 {
		t.Errorf("Expected value 10 after Set(10), got %d", g.Get())
	}

	// Test Inc
	g.Inc()
	if g.Get() != 11 {
		t.Errorf("Expected value 11 after Inc, got %d", g.Get())
	}

	// Test Dec
	g.Dec()
	if g.Get() != 10 {
		t.Errorf("Expected value 10 after Dec, got %d", g.Get())
	}

	// Test Add
	g.Add(5)
	if g.Get() != 15 {
		t.Errorf("Expected value 15 after Add(5), got %d", g.Get())
	}

	// Test Sub
	g.Sub(3)
	if g.Get() != 12 {
		t.Errorf("Expected value 12 after Sub(3), got %d", g.Get())
	}

	// Test Set to negative value
	g.Set(-5)
	if g.Get() != -5 {
		t.Errorf("Expected value -5 after Set(-5), got %d", g.Get())
	}
}

// TestLocalHistogram tests the localHistogram implementation
func TestLocalHistogram(t *testing.T) {
	h := &localHistogram{}

	// Histogram's Observe method is a no-op, just ensure it doesn't panic
	h.Observe(100)
	h.Observe(0)
	h.Observe(-50)
}

// TestLocalRegistryRegisterCounter tests the RegisterCounter method
func TestLocalRegistryRegisterCounter(t *testing.T) {
	registry := newLocalRegistry()

	// Register a new counter
	counter1 := registry.RegisterCounter("test_counter", "Test counter help", nil).WithLabels(nil)
	if counter1 == nil {
		t.Fatal("Expected counter to be created, got nil")
	}

	// Verify it's in the registry
	if len(registry.counters) != 1 {
		t.Errorf("Expected 1 counter in registry, got %d", len(registry.counters))
	}

	// Register the same counter again - should return existing one
	counter2 := registry.RegisterCounter("test_counter", "Different help text", nil).WithLabels(nil)
	if counter1 != counter2 {
		t.Error("Expected RegisterCounter to return existing counter for same name")
	}

	// Verify still only one counter
	if len(registry.counters) != 1 {
		t.Errorf("Expected 1 counter in registry after re-registration, got %d", len(registry.counters))
	}

	// Register a different counter
	counter3 := registry.RegisterCounter("another_counter", "Another counter", nil).WithLabels(nil)
	if counter1 == counter3 {
		t.Error("Expected different counter instances for different names")
	}

	if len(registry.counters) != 2 {
		t.Errorf("Expected 2 counters in registry, got %d", len(registry.counters))
	}
}

// TestLocalRegistryRegisterGauge tests the RegisterGauge method
func TestLocalRegistryRegisterGauge(t *testing.T) {
	registry := newLocalRegistry()

	// Register a new gauge
	gauge1 := registry.RegisterGauge("test_gauge", "Test gauge help", nil).WithLabels(nil)
	if gauge1 == nil {
		t.Fatal("Expected gauge to be created, got nil")
	}

	// Verify it's in the registry
	if len(registry.gauges) != 1 {
		t.Errorf("Expected 1 gauge in registry, got %d", len(registry.gauges))
	}

	// Register the same gauge again - should return existing one
	gauge2 := registry.RegisterGauge("test_gauge", "Different help text", nil).WithLabels(nil)
	if gauge1 != gauge2 {
		t.Error("Expected RegisterGauge to return existing gauge for same name")
	}

	// Verify still only one gauge
	if len(registry.gauges) != 1 {
		t.Errorf("Expected 1 gauge in registry after re-registration, got %d", len(registry.gauges))
	}

	// Register a different gauge
	gauge3 := registry.RegisterGauge("another_gauge", "Another gauge", nil).WithLabels(nil)
	if gauge1 == gauge3 {
		t.Error("Expected different gauge instances for different names")
	}

	if len(registry.gauges) != 2 {
		t.Errorf("Expected 2 gauges in registry, got %d", len(registry.gauges))
	}
}

// TestLocalRegistryRegisterHistogram tests the RegisterHistogram method
func TestLocalRegistryRegisterHistogram(t *testing.T) {
	registry := newLocalRegistry()

	// Register a new histogram
	labeledHist1 := registry.RegisterHistogram("test_histogram", "Test histogram help", []int64{1, 2, 3}, nil)
	if labeledHist1 == nil {
		t.Fatal("Expected labeled histogram to be created, got nil")
	}

	// Get the actual histogram instance with WithLabels
	histogram1 := labeledHist1.WithLabels(nil)
	if histogram1 == nil {
		t.Fatal("Expected histogram to be created, got nil")
	}

	// Verify it's in the registry
	if len(registry.histograms) != 1 {
		t.Errorf("Expected 1 histogram in registry, got %d", len(registry.histograms))
	}

	// Register the same histogram again - calling WithLabels should return the same instance
	histogram2 := registry.RegisterHistogram("test_histogram", "Different help text", []int64{5, 10, 15}, nil).WithLabels(nil)
	if histogram1 != histogram2 {
		t.Error("Expected same histogram instance for same name and labels")
	}

	// Verify still only one histogram
	if len(registry.histograms) != 1 {
		t.Errorf("Expected 1 histogram in registry after re-registration, got %d", len(registry.histograms))
	}

	// Register and use a different histogram
	registry.RegisterHistogram("another_histogram", "Another histogram", nil, nil).WithLabels(nil)

	if len(registry.histograms) != 2 {
		t.Errorf("Expected 2 histograms in registry, got %d", len(registry.histograms))
	}

	// Verify both histograms are in the registry by key
	// Note: keys are now the full metric keys including label info
	if len(registry.histograms) < 2 {
		t.Error("Expected both histograms to be in registry")
	}
}

// TestLocalRegistryCounterFunctionality tests that registered counters work correctly
func TestLocalRegistryCounterFunctionality(t *testing.T) {
	registry := newLocalRegistry()
	counter := registry.RegisterCounter("functional_counter", "Test", nil).WithLabels(nil)

	counter.Inc()
	if counter.Get() != 1 {
		t.Errorf("Expected counter value 1, got %d", counter.Get())
	}

	counter.Add(10)
	if counter.Get() != 11 {
		t.Errorf("Expected counter value 11, got %d", counter.Get())
	}
}

// TestLocalRegistryGaugeFunctionality tests that registered gauges work correctly
func TestLocalRegistryGaugeFunctionality(t *testing.T) {
	registry := newLocalRegistry()
	gauge := registry.RegisterGauge("functional_gauge", "Test", nil).WithLabels(nil)

	gauge.Set(100)
	if gauge.Get() != 100 {
		t.Errorf("Expected gauge value 100, got %d", gauge.Get())
	}

	gauge.Inc()
	if gauge.Get() != 101 {
		t.Errorf("Expected gauge value 101, got %d", gauge.Get())
	}

	gauge.Dec()
	if gauge.Get() != 100 {
		t.Errorf("Expected gauge value 100, got %d", gauge.Get())
	}
}

// TestLocalRegistryConcurrentAccess tests thread-safety of the localRegistry
func TestLocalRegistryConcurrentAccess(t *testing.T) {
	registry := newLocalRegistry()
	var wg sync.WaitGroup

	// Number of concurrent goroutines
	numGoroutines := 100

	// Test concurrent counter registration
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			// All goroutines try to register the same counter
			counter := registry.RegisterCounter("shared_counter", "Test", nil).WithLabels(nil)
			counter.Inc()
		}(i)
	}
	wg.Wait()

	// Should have exactly one counter
	if len(registry.counters) != 1 {
		t.Errorf("Expected 1 counter after concurrent registration, got %d", len(registry.counters))
	}

	// Counter should have been incremented by all goroutines
	counter := registry.RegisterCounter("shared_counter", "Test", nil).WithLabels(nil)
	if counter.Get() != int64(numGoroutines) {
		t.Errorf("Expected counter value %d, got %d", numGoroutines, counter.Get())
	}

	// Test concurrent gauge registration
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			gauge := registry.RegisterGauge("shared_gauge", "Test", nil).WithLabels(nil)
			gauge.Inc()
		}(i)
	}
	wg.Wait()

	// Should have exactly one gauge
	if len(registry.gauges) != 1 {
		t.Errorf("Expected 1 gauge after concurrent registration, got %d", len(registry.gauges))
	}

	// Test concurrent histogram registration
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			histogram := registry.RegisterHistogram("shared_histogram", "Test", nil, nil).WithLabels(nil)
			histogram.Observe(int64(id))
		}(i)
	}
	wg.Wait()

	// Should have exactly one histogram
	if len(registry.histograms) != 1 {
		t.Errorf("Expected 1 histogram after concurrent registration, got %d", len(registry.histograms))
	}
}

// TestLocalRegistryMultipleMetrics tests registering multiple different metrics
func TestLocalRegistryMultipleMetrics(t *testing.T) {
	registry := newLocalRegistry()

	// Register multiple counters
	for i := 0; i < 5; i++ {
		name := "counter_" + string(rune('a'+i))
		registry.RegisterCounter(name, "Test counter", nil).WithLabels(nil)
	}

	// Register multiple gauges
	for i := 0; i < 5; i++ {
		name := "gauge_" + string(rune('a'+i))
		registry.RegisterGauge(name, "Test gauge", nil).WithLabels(nil)
	}

	// Register multiple histograms
	for i := 0; i < 5; i++ {
		name := "histogram_" + string(rune('a'+i))
		registry.RegisterHistogram(name, "Test histogram", nil, nil).WithLabels(nil)
	}

	if len(registry.counters) != 5 {
		t.Errorf("Expected 5 counters, got %d", len(registry.counters))
	}

	if len(registry.gauges) != 5 {
		t.Errorf("Expected 5 gauges, got %d", len(registry.gauges))
	}

	if len(registry.histograms) != 5 {
		t.Errorf("Expected 5 histograms, got %d", len(registry.histograms))
	}
}

// TestLocalCounterConcurrentIncrement tests concurrent increments on a counter
func TestLocalCounterConcurrentIncrement(t *testing.T) {
	counter := &localCounter{}
	var wg sync.WaitGroup
	numGoroutines := 1000
	incrementsPerGoroutine := 100

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < incrementsPerGoroutine; j++ {
				counter.Inc()
			}
		}()
	}
	wg.Wait()

	expected := int64(numGoroutines * incrementsPerGoroutine)
	if counter.Get() != expected {
		t.Errorf("Expected counter value %d, got %d", expected, counter.Get())
	}
}

// TestLocalGaugeConcurrentOperations tests concurrent operations on a gauge
func TestLocalGaugeConcurrentOperations(t *testing.T) {
	gauge := &localGauge{}
	var wg sync.WaitGroup
	numGoroutines := 100

	// Set initial value
	gauge.Set(0)

	// Half goroutines increment, half decrement
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		if i%2 == 0 {
			go func() {
				defer wg.Done()
				gauge.Inc()
			}()
		} else {
			go func() {
				defer wg.Done()
				gauge.Dec()
			}()
		}
	}
	wg.Wait()

	// With equal increments and decrements, value should be 0
	if gauge.Get() != 0 {
		t.Errorf("Expected gauge value 0 after equal Inc/Dec operations, got %d", gauge.Get())
	}
}

// TestLabelsToString tests the labelsToString function
func TestLabelsToString(t *testing.T) {
	tests := []struct {
		name     string
		labels   Labels
		expected string
	}{
		{
			name:     "empty labels",
			labels:   nil,
			expected: "",
		},
		{
			name:     "single label",
			labels:   Labels{"method": "GET"},
			expected: `method="GET"`,
		},
		{
			name:     "multiple labels sorted",
			labels:   Labels{"method": "GET", "status": "200"},
			expected: `method="GET",status="200"`,
		},
		{
			name:     "multiple labels reverse order",
			labels:   Labels{"status": "200", "method": "GET"},
			expected: `method="GET",status="200"`, // Should be sorted
		},
		{
			name:     "labels with special characters",
			labels:   Labels{"path": "/api/v1/users", "method": "POST"},
			expected: `method="POST",path="/api/v1/users"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := labelsToString(tt.labels)
			if result != tt.expected {
				t.Errorf("labelsToString() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

// TestMakeMetricKey tests the makeMetricKey function
func TestMakeMetricKey(t *testing.T) {
	tests := []struct {
		name     string
		metricName string
		labels   Labels
		expected string
	}{
		{
			name:       "no labels",
			metricName: "http_requests_total",
			labels:     nil,
			expected:   "http_requests_total",
		},
		{
			name:       "with labels",
			metricName: "http_requests_total",
			labels:     Labels{"method": "GET", "status": "200"},
			expected:   `http_requests_total{method="GET",status="200"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := makeMetricKey(tt.metricName, tt.labels)
			if result != tt.expected {
				t.Errorf("makeMetricKey() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

// TestLocalRegistryWithLabels tests that metrics with different labels are separate
func TestLocalRegistryWithLabels(t *testing.T) {
	registry := newLocalRegistry()

	// Register a counter with label names
	httpRequests := registry.RegisterCounter("http_requests", "HTTP requests", []string{"method"})

	// Use WithLabels to get counters for different label values
	counter1 := httpRequests.WithLabels(Labels{"method": "GET"})
	counter2 := httpRequests.WithLabels(Labels{"method": "POST"})

	// Register a counter without labels
	totalRequests := registry.RegisterCounter("total_requests", "Total requests", nil)
	counter3 := totalRequests.WithLabels(nil)

	// Increment each counter by different amounts
	counter1.Add(10)
	counter2.Add(20)
	counter3.Add(30)

	// Verify values are independent
	if counter1.Get() != 10 {
		t.Errorf("Expected counter1 value 10, got %d", counter1.Get())
	}
	if counter2.Get() != 20 {
		t.Errorf("Expected counter2 value 20, got %d", counter2.Get())
	}
	if counter3.Get() != 30 {
		t.Errorf("Expected counter3 value 30, got %d", counter3.Get())
	}

	// Verify all three are in the registry
	if len(registry.counters) != 3 {
		t.Errorf("Expected 3 counters in registry, got %d", len(registry.counters))
	}
}

// TestLocalRegistryGaugeWithLabels tests gauges with labels
func TestLocalRegistryGaugeWithLabels(t *testing.T) {
	registry := newLocalRegistry()

	// Register a gauge with label names
	temperature := registry.RegisterGauge("temperature", "Temperature", []string{"location"})

	// Use WithLabels to get gauges for different label values
	gauge1 := temperature.WithLabels(Labels{"location": "indoor"})
	gauge2 := temperature.WithLabels(Labels{"location": "outdoor"})

	// Set different values
	gauge1.Set(20)
	gauge2.Set(15)

	// Verify values are independent
	if gauge1.Get() != 20 {
		t.Errorf("Expected gauge1 value 20, got %d", gauge1.Get())
	}
	if gauge2.Get() != 15 {
		t.Errorf("Expected gauge2 value 15, got %d", gauge2.Get())
	}

	// Verify both are in the registry
	if len(registry.gauges) != 2 {
		t.Errorf("Expected 2 gauges in registry, got %d", len(registry.gauges))
	}
}

// TestLocalRegistryHistogramWithLabels tests histograms with labels
func TestLocalRegistryHistogramWithLabels(t *testing.T) {
	registry := newLocalRegistry()

	// Register a histogram with label names
	responseTime := registry.RegisterHistogram("response_time", "Response time", []int64{100, 200, 500}, []string{"endpoint"})

	// Use WithLabels to get histograms for different label values
	hist1 := responseTime.WithLabels(Labels{"endpoint": "/api"})
	hist2 := responseTime.WithLabels(Labels{"endpoint": "/health"})

	// Observe values (no-ops for local implementation, but verifies no panic)
	hist1.Observe(150)
	hist2.Observe(50)

	// Verify both are in the registry as separate entries
	if len(registry.histograms) != 2 {
		t.Errorf("Expected 2 histograms in registry, got %d", len(registry.histograms))
	}

	// Verify we can retrieve them again with the same labels
	hist1Again := responseTime.WithLabels(Labels{"endpoint": "/api"})
	hist2Again := responseTime.WithLabels(Labels{"endpoint": "/health"})

	// Should get the same instances back
	if hist1 != hist1Again {
		t.Error("Expected to get same histogram instance for same labels")
	}
	if hist2 != hist2Again {
		t.Error("Expected to get same histogram instance for same labels")
	}
}

// TestLocalRegistrySameLabelsDifferentOrder tests that label order doesn't matter
func TestLocalRegistrySameLabelsDifferentOrder(t *testing.T) {
	registry := newLocalRegistry()

	// Register counter with label names
	requests := registry.RegisterCounter("requests", "Requests", []string{"method", "status"})

	// Get counter with labels in one order
	counter1 := requests.WithLabels(Labels{"method": "GET", "status": "200"})
	counter1.Add(5)

	// Get counter with same labels in different order
	counter2 := requests.WithLabels(Labels{"status": "200", "method": "GET"})

	// Should return the same counter
	if counter1 != counter2 {
		t.Error("Expected same counter for same labels in different order")
	}

	// Value should be preserved
	if counter2.Get() != 5 {
		t.Errorf("Expected counter value 5, got %d", counter2.Get())
	}

	// Should only have one counter in registry
	if len(registry.counters) != 1 {
		t.Errorf("Expected 1 counter in registry, got %d", len(registry.counters))
	}
}
