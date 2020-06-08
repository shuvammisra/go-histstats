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
   // "fmt"
    "os"
    "io"
    "io/ioutil"
    "time"
    "errors"
    // "sync"
//    "encoding/json"
)

const (
    MONITOR_CHANNEL_DEPTH int	= 10	// increase and re-compile if needed
    DATAFETCH_SLEEP_SEC	  int	= 2
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
    T		entrytype	`json:"type"`
    Elabel	entryLabel_t	// "entry label", telling you which time slice this reading is for
    Count	Count_t
    Val		Val_t
}

//
// One HistLog object can hold one set of data points for historical statistics
// of one attribute.
//
type HistLog struct {
    lastChecked	time.Time	// timestamp of when we last checked for wraps
    Logs	[]histlogentry
    Day2Month	bool	// true means days collapse to months, false means days collapse to weeks
    Weekstart	time.Weekday	// Sun or Mon in most places, Fri in some Middle Eastern countries
    Agelimit	int	// in years, typically a small single-digit figure
    storePath	string	// filename for store
    pendingLogs	int	// incremented each Log(), reset on each Flush()
    Autoflush	int	// if not nil, it means auto-flush every so many updates
    Club2mins	bool	// true ==> club current readings at minute boundaries, track individual minutes
			// false ==> club them at hour boundaries,
    closed	bool	// set to true after a Close() is called on the object
    tomonitor	chan monmsg
}

//
// Message types for messages sent to the monitor daemon thread
//
type mTypes	int
const (
    mTypeFlush	mTypes	= iota
    mTypeIncr			// used to do a pure-count-incr logging
    mTypeLog			// used to log an entry
    mTypeSetFlushFile		// sets the flush-to filename
    mTypeGet			// used for the read-from-file operation
    mTypeSet			// used for write-to-file operation
    mTypeClose			// monitor-thread winds up and exits
)
//
// Messages sent to the monitor-daemon thread ... oops, goroutine
//
// These are one-way messages. An out-parameter sometimes comes back
// via the strparam, which is a pointer to string, and which the monitor
// thread will set with a string value, in the case of the Get call.
//
type monmsg struct {
    mtype	mTypes
    count	Count_t
    val		Val_t
    strparam	*string
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
//	club2mins: false means you are not interested in minute-level
//		data for the last hour, true means you want current
//		readings to be clubbed into minutes at minute boundaries
//		first, then those minutes get clubbed into hours at hour
//		boundaries
//
func NewHistLog(d2m bool, weekstart time.Weekday, agelimit, af int,
	club2mins bool) (l HistLog) {
    l.lastChecked	= time.Now()
    l.Logs		= make([]histlogentry, 20)
    l.Day2Month		= d2m
    l.Weekstart		= weekstart
    l.Agelimit		= agelimit
//    l.storePath		= nil
    l.pendingLogs	= 0
    l.Autoflush		= af
    l.Club2mins		= club2mins
    l.closed		= false
    l.tomonitor		= make(chan monmsg, MONITOR_CHANNEL_DEPTH)

    return l
}

//
// Called to instantiate and load a HistLog from its backing store (a file);
// this file name is remembered for later Flush() calls
//
func Load(filename string) (l *HistLog, err error) {
    return l, err
}

//
// Called to instantiate and read a HistLog object from any I/O endpoint.
// A HistLog instance created by Read() does not have its flush-store
// filename set, so calls to Flush() will fail unless you set the
// filename by making a call to SetFlushFile() first
//
// Why does Load() set the flush-to filename of the instance created, but 
// Read() does not? Because Load() takes a filename on disk as parameter 
// to load from, whereas Read() is more generic and reads from any readable 
// stream endpoint.
//
func Read(io.Reader) (l *HistLog, err error) {
    return l, err
}


//
// The monitor function which runs in a separate goroutine and performs
// all actual operations on the HistLog instance. One goroutine operates per
// active HistLog instance.
//
func monitor(l *HistLog) {
    const MONITOR_EXIT_EMPTY_LOOPS int = -2	// needs to be -ve
    var gotAnOrder	int

    for {
	select {
	case msg := <-(*l).tomonitor:
	    if ((*l).closed) {
		gotAnOrder = 1
		continue
	    }
	    switch msg.mtype {
	    case mTypeFlush: {
		// do a json.marshall, then flush the string to file
	    }
	    case mTypeIncr: {
		loginternal(l, msg.count, Val_t(0))
	    }
	    case mTypeLog: {
		loginternal(l, msg.count, msg.val)
	    }
	    case mTypeSetFlushFile: {
		if (msg.strparam != nil) && ((*msg.strparam) != "") {
		    (*l).storePath = (*msg.strparam)
		} else {
		    panic("call to SetFlushFile with empty flush filename")
		}
	    }
	    case mTypeGet:
	    case mTypeSet:
	    case mTypeClose:
		flushinternal(l)
		(*l).closed = true
		(*l).Logs	= nil	// free up space
	    }
	case <-time.After(time.Minute):
	    if ((*l).closed) {
		gotAnOrder--
		//
		// if the HistLog instance is closed and we've run
		// through a few empty loops just marking the minutes, we
		// know that all the client threads which may have been
		// queueing messages in the channel are now done, and no
		// new messages will be coming in. So we can exit the
		// goroutine safely.
		//
		if gotAnOrder <  MONITOR_EXIT_EMPTY_LOOPS {
		    return
		}
		continue
	    }
	    doWrap(l)
	    if (*l).pendingLogs > (*l).Autoflush {
		flushinternal(l)
		(*l).pendingLogs = 0
	    }
	} // select
    } // infinite for loop
}

//
// This critical internal function detects if there has been any wrapping
// of any of the time units (year, month, etc) between the last checked
// time and now. If there has been, it does a collapsing of all records
// which need collapsing. This is the heart of the package. If this works
// correctly, Covid19 lockdowns are a pot of payasam.
//
func doWrap(lptr *HistLog) {

    // very critical check to see if time has moved backwards, as often
    // happens with system clock being reset or timezone changes. We work
    // with local time, which can move back and forth.
    if time.Now().Sub((*lptr).lastChecked) < 0 {

	// if time has indeed moved backwards, we will just do nothing in
	// each call to doWrap(), and bide our time till time once again
	// moves forward, i.e. current system time is "after" l.lastChecked
	return
    }

    t := time.Now()
    lastyr, lastmth, lastdate := (*lptr).lastChecked.Date()
    var lastweek	int
    if !(*lptr).Day2Month {
	lastyr, lastweek = (*lptr).lastChecked.ISOWeek()
	lastdate = int((*lptr).lastChecked.Weekday())
    }
    lasthr, lastmin, _  := (*lptr).lastChecked.Clock()

    nowyr, nowmth, nowdate    := t.Date()
    var nowweek		int
    if !(*lptr).Day2Month {
	nowyr, nowweek = t.ISOWeek()
	nowdate = int(t.Weekday())
    }
    nowhr, nowmin, _     := t.Clock()

    var wraptype entrytype

    if nowyr > lastyr {		// the year has wrapped since last time
	wraptype = etypeYear
    } else if (*lptr).Day2Month {
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
	logEntryCollapse(lptr, etypeRaw, etypeMinute)
    }
    if wraptype >= etypeHour {
	logEntryCollapse(lptr, etypeMinute, etypeHour)
    }
    if wraptype >= etypeDay {
	logEntryCollapse(lptr, etypeHour, etypeDay)
    }
    if (*lptr).Day2Month {
	if wraptype >= etypeMonth {
	    logEntryCollapse(lptr, etypeDay, etypeMonth)
	    if wraptype >= etypeYear {
		logEntryCollapse(lptr, etypeMonth, etypeYear)
	    }
	}
    } else {
	if wraptype >= etypeWeek {
	    logEntryCollapse(lptr, etypeDay, etypeWeek)
	    if wraptype >= etypeYear {
		logEntryCollapse(lptr, etypeWeek, etypeYear)
	    }
	}
    }

    (*lptr).lastChecked = t
}  // end doWrap()

//
// Collapses all the log entries of a specific type and replaces
// them with a new entry of the next "fatter" type.
//
func logEntryCollapse(lptr *HistLog, wrapfrom, collapseto entrytype) {
    var totalcount Count_t
    var totalvals  Val_t
    var thisentry histlogentry
    var lastyr, lastmth, lastweek, lastdate, lasthr, lastmin	int

    {
	var acertainmonth time.Month
	lastyr, acertainmonth, lastdate = (*lptr).lastChecked.Date()
	lastmth = int(acertainmonth)
    }

    if !(*lptr).Day2Month {
	lastyr, lastweek = (*lptr).lastChecked.ISOWeek()
	lastdate = int((*lptr).lastChecked.Weekday())
    }
    lasthr, lastmin, _  = (*lptr).lastChecked.Clock()

    // We aggregate all the lower-level entries...
    for _, thisentry = range (*lptr).Logs {
	if thisentry.T == wrapfrom {
	    totalcount += thisentry.Count
	    totalvals  += thisentry.Val
	}
    }
    // ... and create a new entry
    thisentry.Count = totalcount
    thisentry.Val   = totalvals
    thisentry.T     = collapseto

    onelabel := entryLabel_t{ int16(lastyr), int16(lastweek), int16(lastdate),
	    int16(lasthr), int16(lastmin) }
    thisentry.Elabel = onelabel
    if (*lptr).Day2Month {
	thisentry.Elabel[1] = int16(lastmth)
    }
    (*lptr).Logs = append((*lptr).Logs, thisentry)
} // end logEntryCollapse()

//
// Internal function to add a log entry to a HistLog instance. Called
// from the monitor goroutine.
//
func loginternal(l *HistLog, count Count_t, val Val_t) {
    var thisentry	histlogentry
    var now		time.Time = time.Now()
    var nowyr, nowmth, nowweek, nowdate, nowhr, nowmin	int

    {
	var acertainmonth time.Month
	nowyr, acertainmonth, nowdate = now.Date()
	nowmth = int(acertainmonth)
    }
    nowhr, nowmin, _ = now.Clock()
    if !(*l).Day2Month {
	nowyr, nowweek = now.ISOWeek()
	nowdate = int(now.Weekday())
    }
    onelabel := entryLabel_t{ int16(nowyr), int16(nowmth),
		int16(nowdate), int16(nowhr), int16(nowmin) }
    if !(*l).Day2Month {
	onelabel[1] = int16(nowweek)
    }

    thisentry.T		= etypeRaw
    thisentry.Count	= count
    thisentry.Val	= val
    thisentry.Elabel	= onelabel

    (*l).Logs = append((*l).Logs, thisentry)
    (*l).pendingLogs++
}

//
// Internal function to flush an instance to disk
//
func flushinternal(l *HistLog) {
}

//
// Exported method to add a new entry to a HistLog instance.
//
func (l HistLog) Log(c Count_t, v Val_t) (error) {
    if l.closed {
	return errors.New("HistLog object is closed for business")
    }
    var msg monmsg

    msg.mtype	= mTypeLog
    msg.count	= c
    msg.val		= v
    msg.strparam	= nil

    l.tomonitor <-msg
    return nil
}

//
// Exported method to add a new entry to a HistLog instance with just an
// increment of the count
//
func (l HistLog) IncrBy(c Count_t) (error) {
    if l.closed {
	return errors.New("HistLog object is closed for business")
    }
    var msg monmsg

    msg.mtype	= mTypeIncr
    msg.count	= c
    msg.val		= Val_t(0)
    msg.strparam	= nil

    l.tomonitor <-msg
    return nil
}

//
// Set the flush-to filename.
//
func (l HistLog) SetFlushFile(flushtofilename string) (error) {
    if l.closed {
	return errors.New("HistLog object is closed for business")
    }
    var somebytes = []byte{'a','b','c'}

    // do a trial write to the filename given
    if e := ioutil.WriteFile(flushtofilename, somebytes, 0660); e != nil {
	return e
    }
    if e := os.Remove(flushtofilename); e != nil {
	return e
    }
    var msg monmsg

    msg.mtype		= mTypeSetFlushFile
    msg.count		= Count_t(0)
    msg.val		= Val_t(0)
    *msg.strparam	= flushtofilename

    l.tomonitor<- msg
    return nil
}

//
// Do an explicit flush of the HistLog to its pre-set flush-to file
//
func (l HistLog) Flush() (err error) {
    if l.closed {
	return errors.New("HistLog object is closed for business")
    }
    if l.storePath != "" {
	var msg	monmsg

	msg.mtype		= mTypeFlush
	msg.count		= Count_t(0)
	msg.val		= Val_t(0)
	msg.strparam	= nil

	l.tomonitor<- msg
	return nil
    } else {
	return errors.New("flush-to filename not set")
    }
}

//
// Write out a nicely formatted representation of the HistLog object to
// the given writable stream.
//
func (l HistLog) Write(w io.Writer) (err error) {
    if l.closed {
	return errors.New("HistLog object is closed for business")
    }
    var msg		monmsg
    var fetcheddata	string

    msg.mtype		= mTypeGet
    msg.count		= Count_t(0)
    msg.val		= Val_t(0)
    msg.strparam	= &fetcheddata

    l.tomonitor<- msg
    //
    // We go into a loop of sleeping and waiting, till the monitor
    // goroutine gets us the data we are waiting for and loads it
    // into the string which we've passed by reference.
    //
    for len(*msg.strparam) == 0 {
	time.Sleep(time.Duration(DATAFETCH_SLEEP_SEC) * time.Second)
    }
    time.Sleep(time.Duration(DATAFETCH_SLEEP_SEC) * time.Second) // for good measure

    _, err = w.Write([]byte(*msg.strparam))
    return err
}

//
// Close an object. Declare that it will no longer be in use. If the
// flush-to filename is set, then this will cause a final flush
// operation. If this is not set, then you will lose all entries since
// the last Write() or Flush().
//
// Multiple calls to Close() will not trigger any error. The first one
// will do the closing, the rest will just return quietly.
//
func (l HistLog) Close() {
    if l.closed {
	return
    }
    var msg		monmsg

    msg.mtype		= mTypeClose
    msg.count		= Count_t(0)
    msg.val		= Val_t(0)
    msg.strparam	= nil

    l.tomonitor<- msg
}
