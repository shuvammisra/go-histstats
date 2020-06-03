// Allows a program to maintain statistics (simple counts or averages of
// values) of any numerical attribute, aged with progressively less
// details for older data. Newer statistics are in full detail, older figures
// are averaged and clubbed into bins to carry less detail
//
// Data retained
//
// Each reading logged in the last few seconds is retained separately.
// At minute boundaries, all the readings logged in the last 60 seconds
// is averaged and aggregated into one reading for that minute; the
// individual raw readings are then discarded.
// At hour boundaries, all the most recent minute-level averages are 
// aggregated into a single hour-level reading and the minute-level 
// readings are then discarded.
// At day boundaries, all the most recent hour-level averages are further
// averaged to make a single aggregate day-level reading, and a single
// entry is remembered; all the earlier hour-level readings are discarded.
// And so on.
// Therefore, the HistLog object will have several recent raw readings,
// plus (optionally) up to 59 aggregate minute-level readings, plus up
// to 23 hour-level readings, plus up to 30 day-level readings, plus
// up to 11 month-level readings, plus up to "agelimit" year-level
// readings. So, for agelimit set to 5, there will be a max of
//	5 year-level readings plus
//	11 month-level readings plus
//	30 day-level readings plus
//	23 hour-level readings plus
//	59 minute-level readings plus
//	any number of raw readings
//	= 128 + most recent raw readings.
// That's not too big for 5 whole years.
//
// Loading and storing
//
// One can load and store a HistLog object from a file, or read/write it from
// any I/O endpoint like a TCP socket or Unix pipe.
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
    isdirty	bool	// true ==> a flush is needed
    autoflush	int	// if not nil, it means auto-flush every so many updates
    clubtomins bool	// true ==> club current readings at minute boundaries, track individual minutes
    			// false ==> club them at hour boundaries,
}

// Called when you want to create a new HistLog instance from scratch
//
//	d2m: true means you want days to collapse to months, false
//		means days collapse to weeks, and 52 weeks a year
//	weekstart: set it to Sunday, Monday or Friday to decide when
//		to collapse days to a week. Only relevant if d2m == false
//	agelimit: set it to a small integer to decide how many years of
//		data to remember
//	af: nil means no auto-flush, an integer means auto-flush after
//		that many Log() or Incr() calls
//	clubtomins: false means you are not interested in minute-level
//		data for the last hour, true means you want current
//		readings to be clubbed into minutes at minute boundaries
//		first, then those minutes get clubbed into hours at hour
//		boundaries
func NewHistLog(d2m bool, weekstart DoW, agelimit, af int, clubtomins bool) (l HistLog) { }

// Called to load a HistLog from its backing store (a file), and this
// file name is remembered for later Flush() calls
func Load(filename string) (l HistLog, err error) { }

// Called to load a HistLog object from a file from any I/O endpoint
func Read(io.Reader) (l HistLog, err error) { }

func (l HistLog) IncrBy(i int) (int) { }
func (l HistLog) Log(count, val int) { }
func (l HistLog) Flush() (err error) { }
func (l HistLog) Write(io.Writer) (err error) { }
func (l HistLog) SetFlushToFile(io.Writer) (err error) { }
