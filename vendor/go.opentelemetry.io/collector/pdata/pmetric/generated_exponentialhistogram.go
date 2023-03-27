// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated by "pdata/internal/cmd/pdatagen/main.go". DO NOT EDIT.
// To regenerate this file run "make genpdata".

package pmetric

import (
	otlpmetrics "go.opentelemetry.io/collector/pdata/internal/data/protogen/metrics/v1"
)

// ExponentialHistogram represents the type of a metric that is calculated by aggregating
// as a ExponentialHistogram of all reported double measurements over a time interval.
//
// This is a reference type, if passed by value and callee modifies it the
// caller will see the modification.
//
// Must use NewExponentialHistogram function to create new instances.
// Important: zero-initialized instance is not valid for use.
type ExponentialHistogram struct {
	orig *otlpmetrics.ExponentialHistogram
}

func newExponentialHistogram(orig *otlpmetrics.ExponentialHistogram) ExponentialHistogram {
	return ExponentialHistogram{orig}
}

// NewExponentialHistogram creates a new empty ExponentialHistogram.
//
// This must be used only in testing code. Users should use "AppendEmpty" when part of a Slice,
// OR directly access the member if this is embedded in another struct.
func NewExponentialHistogram() ExponentialHistogram {
	return newExponentialHistogram(&otlpmetrics.ExponentialHistogram{})
}

// MoveTo moves all properties from the current struct overriding the destination and
// resetting the current instance to its zero value
func (ms ExponentialHistogram) MoveTo(dest ExponentialHistogram) {
	*dest.orig = *ms.orig
	*ms.orig = otlpmetrics.ExponentialHistogram{}
}

// AggregationTemporality returns the aggregationtemporality associated with this ExponentialHistogram.
func (ms ExponentialHistogram) AggregationTemporality() AggregationTemporality {
	return AggregationTemporality(ms.orig.AggregationTemporality)
}

// SetAggregationTemporality replaces the aggregationtemporality associated with this ExponentialHistogram.
func (ms ExponentialHistogram) SetAggregationTemporality(v AggregationTemporality) {
	ms.orig.AggregationTemporality = otlpmetrics.AggregationTemporality(v)
}

// DataPoints returns the DataPoints associated with this ExponentialHistogram.
func (ms ExponentialHistogram) DataPoints() ExponentialHistogramDataPointSlice {
	return newExponentialHistogramDataPointSlice(&ms.orig.DataPoints)
}

// CopyTo copies all properties from the current struct overriding the destination.
func (ms ExponentialHistogram) CopyTo(dest ExponentialHistogram) {
	dest.SetAggregationTemporality(ms.AggregationTemporality())
	ms.DataPoints().CopyTo(dest.DataPoints())
}
