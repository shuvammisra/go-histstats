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
// Note that this module does not record logs "for an hour" and then
// collapse them into a single hour-level entry, or record "for a minute"
// and collapse to a minute-level entry. It does its collapsing when the
// clock time shows a transition to the next hour or next minute, etc. So,
// "a month" is not just so many days -- it starts at the beginning of a
// calendar month and wraps to the next month at the time the month moves
// to the next, as per the local date and time of the system where the
// software is running. In our language, the minutes, hours, etc, "wrap"
// to the next minute, next hour, etc.
//
// Loading and storing
//
// One can load and store a HistLog object from a file, or read/write it from
// any I/O endpoint like a TCP socket or Unix pipe.
package histstats

import (
    "io"
    "time"
)

// Each log entry in the HistLog set will have a type, which will be one
// of these types
type entrytype int
const (
    etypeRaw		entrytype = iota
    etypeMinute
    etypeHour
    etypeDay
    etypeWeek
    etypeMonth
    etypeYear
)

type Count_t	int64
type Val_t	int64
type entryLabel_t	[5]int16

type histlogentry struct {
    t		entrytype
    elabel	entryLabel_t	// "entry label", telling you which time slice this reading is for
    count	Count_t
    val		Val_t
}

//
// One HistLog object can hold one set of data points for historical statistics
// of one attribute.
//
type HistLog struct {
    lastchecked	time.Time	// timestamp of when we last checked for wraps
    logs	[]histlogentry
    day2month	bool	// true means days collapse to months, false means days collapse to weeks
    weekstart	time.Weekday	// Sun or Mon in most places, Fri in some Middle Eastern countries
    agelimit	int	// in years, typically a small single-digit figure
    store	io.ReadWriteCloser	// where the object is stored in file
    storePath	string	// filename for store
    isdirty	bool	// true ==> a flush is needed
    autoflush	int	// if not nil, it means auto-flush every so many updates
    clubtomins bool	// true ==> club current readings at minute boundaries, track individual minutes
    			// false ==> club them at hour boundaries,
}


//
// Called when you want to create a new HistLog instance from scratch
//
//	d2m: true means you want days to collapse to months, false
//		means days collapse to weeks, and 52 weeks a year
//	weekstart: set it to Sunday, Monday or Friday to decide when
//		to collapse days into a week. Only relevant if d2m == false
//	agelimit: set it to a small integer to decide how many years of
//		data to remember
//	af: nil means no auto-flush, an integer means auto-flush after
//		that many Log() or Incr() calls
//	clubtomins: false means you are not interested in minute-level
//		data for the last hour, true means you want current
//		readings to be clubbed into minutes at minute boundaries
//		first, then those minutes get clubbed into hours at hour
//		boundaries
//
// func NewHistLog(d2m bool, weekstart time.Weekday, agelimit, af int, clubtomins bool) (l HistLog) { }

//
// Called to instantiate and load a HistLog from its backing store (a file);
// this file name is remembered for later Flush() calls
//
// func Load(filename string) (l HistLog, err error) { }

//
// Called to instantiate and read a HistLog object from any I/O endpoint
// A HistLog instance created by Read() does not have its flush-store
// filename set, so calls to Flush() will fail unless you set the
// filename by making a call to SetFlushToFile() first
//
// func Read(io.Reader) (l HistLog, err error) { }

//
// This critical internal function detects if there has been any wrapping
// of any of the time units (year, month, etc) between the last checked
// time and now. If there has been, it does a collapsing of all records
// which need collapsing. This is the heart of the package. If this works
// correctly, Covid19 lockdowns are a pot of payasam.
//
func (l HistLog) doWrap() {

    // very critical check to see if time has moved backwards, as often
    // happens with system clock being reset or timezone changes. We work
    // with local time, which can move back and forth.
    if time.Now().Sub(l.lastchecked) < 0 {

	// if time has indeed moved backwards, we will just do nothing in
	// each call to doWrap(), and bide our time till time once again
	// moves forward, i.e. current system time is "after" l.lastchecked
	return
    }

    t := time.Now()
    lastyr, lastmth, lastdate := l.lastchecked.Date()
    var lastweek	int
    if !l.day2month {
	lastyr, lastweek = l.lastchecked.ISOWeek()
	lastdate = int(l.lastchecked.Weekday())
    }
    lasthr, lastmin, _  := l.lastchecked.Clock()

    nowyr, nowmth, nowdate    := t.Date()
    var nowweek		int
    if !l.day2month {
	nowyr, nowweek = t.ISOWeek()
	nowdate = int(t.Weekday())
    }
    nowhr, nowmin, _     := t.Clock()

    var wraptype entrytype

    if nowyr > lastyr {		// the year has wrapped since last time
	wraptype = etypeYear
    } else if l.day2month {
	if nowmth > lastmth {
	    wraptype = etypeMonth
	} else if nowdate > lastdate {
	    wraptype = etypeDay
	} else if nowhr > lasthr {
	    wraptype = etypeHour
	} else if nowmin > lastmin {
	    wraptype = etypeMinute
	}
    } else {
	if nowweek > lastweek {
	    wraptype = etypeWeek
	} else if nowdate > lastdate {
	    wraptype = etypeDay
	} else if nowhr > lasthr {
	    wraptype = etypeHour
	} else if nowmin > lastmin {
	    wraptype = etypeMinute
	}
    }


    if wraptype >= etypeMinute {
	logEntryCollapse(&l, etypeRaw, etypeMinute)
    }
    if wraptype >= etypeHour {
	logEntryCollapse(&l, etypeMinute, etypeHour)
    }
    if wraptype >= etypeDay {
	logEntryCollapse(&l, etypeHour, etypeDay)
    }
    if l.day2month {
	if wraptype >= etypeMonth {
	    logEntryCollapse(&l, etypeDay, etypeMonth)
	    if wraptype >= etypeYear {
		logEntryCollapse(&l, etypeMonth, etypeYear)
	    }
	}
    } else {
	if wraptype >= etypeWeek {
	    logEntryCollapse(&l, etypeDay, etypeWeek)
	    if wraptype >= etypeYear {
		logEntryCollapse(&l, etypeWeek, etypeYear)
	    }
	}
    }

    l.lastchecked = t
}

//
// Collapses all the log entries of a specific type and replaces
// them with a new entry of the next "fatter" type.
//
func logEntryCollapse(lptr *HistLog, wrapfrom, collapseto entrytype) {
    var totalcount Count_t
    var totalvals  Val_t
    var thisentry histlogentry
    var lastyr, lastmth, lastweek, lastdate, lasthr, lastmin	int
    var acertainmonth time.Month

    lastyr, acertainmonth, lastdate = (*lptr).lastchecked.Date()
    lastmth = int(acertainmonth)

    if !(*lptr).day2month {
	lastyr, lastweek = (*lptr).lastchecked.ISOWeek()
	lastdate = int((*lptr).lastchecked.Weekday())
    }
    lasthr, lastmin, _  = (*lptr).lastchecked.Clock()

    // We aggregate all the lower-level entries...
    for _, thisentry = range (*lptr).logs {
	if thisentry.t == wrapfrom {
	    totalcount += thisentry.count
	    totalvals  += thisentry.val
	}
    }
    // ... and create a new entry
    thisentry.count = totalcount
    thisentry.val   = totalvals
    thisentry.t     = collapseto
    // thisentry.elabel entryLabel_t

    onelabel := entryLabel_t{ int16(lastyr), int16(lastweek), int16(lastdate),
	    int16(lasthr), int16(lastmin) }
    thisentry.elabel = onelabel
    if (*lptr).day2month {
	thisentry.elabel[1] = int16(lastmth)
    }
    (*lptr).logs = append((*lptr).logs, thisentry)
}


// func (l HistLog) IncrBy(i int) (int) { }
// func (l HistLog) Log(count, val int) { }
// func (l HistLog) Flush() (err error) { }
// func (l HistLog) Write(io.Writer) (err error) { }
// func (l HistLog) SetFlushToFile(io.Writer) (err error) { }
