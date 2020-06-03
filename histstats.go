// Allows a program to maintain statistics (simple counts or averages of
// values) of any numerical attribute, aged with progressively less
// details over time. Newer statistics are in full detail, older figures
// are averaged and clubbed into bins to carry less detail
//
// Loading and storing
//
// One can load and store a HistLog object from a file, or read it from
// any I/O endpoint.
package histstats

import (
    "io"
)

type DoW int
type Month int

const (
    Sun DoW = iota
    Mon
    Tue
    Wed
    Thu
    Fri
    Sat

    Jan Month = iota
    Feb
    Mar
    Apr
    May
    Jun
    Jul
    Aug
    Sep
    Oct
    Nov
    Dec
)

type countval struct {
    count, val	int
}

// One HistLog object can hold one set of data points for historical statistics
// of one attribute.
type HistLog struct {
    logentries	map[string]countval
    day2month	bool	// true means days collapse to months, false means days collapse to weeks
    weekstart	DoW	// Sun or Mon in most places, Fri in some Middle Eastern countries
    agelimit	int	// in years, typically a small single-digit figure
    store	io.ReadWriteCloser	// where the object is stored in file
    storePath	string	// filename for store
    isdirty	bool	// updated in memory since the last flush
}

// Called when you want to create a new HistLog instance from scratch
func NewHistLog(d2m bool, weekstart DoW, agelimit int) (l HistLog) { }

// Called to load a HistLog from its backing store (a file), and this
// file name is remembered for later Flush() calls
func HistLogLoad(filename string) (l HistLog, err error) { }

// Called to load a HistLog object from a file from any I/O endpoint
func HistLogRead(io.Reader) (l HistLog, err error) { }

func (l HistLog) IncrBy(i int) (int) { }
func (l HistLog) Log(count, val int) { }
func (l HistLog) Flush() (err error) { }
