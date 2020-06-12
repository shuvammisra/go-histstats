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
    "os"
    "io"
    "io/ioutil"
    "time"
    "errors"
    "math"
    "log"
    "encoding/json"
    "strings"
    "sort"
)

const (
    MONITOR_CHANNEL_DEPTH int	= 10	// increase and re-compile if needed
    datafetch_SLEEP_SEC	  int	= 2
)

// Each log entry in the HistLog set will have a type, which will be one
// of these types
type entryType_t int
const (
    etypeRaw		entryType_t = iota
    etypeMinute
    etypeHour
    etypeDay
    etypeWeek
    etypeMonth
    etypeYear
)

type Count_t		int64
type Val_t		int64
type entryLabel_t	[5]int16	// yr, mth/wk, date, hr, min
const (
    elidxYR		int = iota	// the year number
    elidxMTHWK				// the month or week number
    elidxDATE				// the date number
    elidxHR				// the hour number
    elidxMIN				// the minute number
)

type histlogentry struct {
    T		entryType_t	`json:"type"`
    Elabel	entryLabel_t	// "entry label", telling you which time slice this reading is for
    Count	Count_t
    Val		Val_t
}
type logList	[]histlogentry

//
// One HistLog object can hold one set of data points for historical statistics
// of one attribute.
//
type HistLog struct {
    lastChecked	time.Time	// timestamp of when we last checked for wraps
    Logs	logList
    Day2Month	bool	// true means days collapse to months, false means days collapse to weeks
    Weekstart	time.Weekday	// Sun or Mon in most places, Fri in some Middle Eastern countries
    Agelimit	int	// in years, typically a small single-digit figure
    storePath	string	// filename for store
    pendingLogs	int	// incremented each Log(), reset on each Flush()
    Autoflush	int	// if not nil, it means auto-flush every so many updates
    Club2mins	bool	// true ==> club current readings at minute boundaries, track individual minutes
			// false ==> club them at hour boundaries,
    closed	bool	// set to true after a Close() is called on the object
    monitorexitat  time.Time	// when did the monitor exit?
    tomonitor	chan monmsg
}

//
// Message types for messages sent to the monitor goroutine
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
// The monitor function which runs in a separate goroutine and performs
// all actual operations on the HistLog instance. One goroutine operates per
// active HistLog instance.
//
func monitor(l *HistLog) {
    const MONITOR_EXIT_EMPTY_LOOPS int = -2	// needs to be -ve
    var gotAnOrder	int

    log.Println("monitor: DEBUG: started")
    for {
	select {
	case msg := <-(*l).tomonitor:
	    if ((*l).closed) {
		gotAnOrder = 1
		continue
	    }
	    switch msg.mtype {
		case mTypeFlush: {
		    // we've reached here means the flush-to filename has
		    // been set already
		    flushinternal(l)
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
	case <-time.After(monitorLoopDelay):
	    log.Println("monitor: DEBUG: timer wakeup")
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
		    log.Println("monitor: INFO: terminating")
		    (*l).monitorexitat = timeNow()
		    return
		}
		continue
	    }
	    doWrap(l)
	    if ((*l).pendingLogs > (*l).Autoflush) && ((*l).Autoflush > 0) {
		flushinternal(l)
		log.Println("monitor: INFO: did auto-flush")
	    }
	} // select
    } // infinite for loop
} // end monitor()

//
// This critical internal function detects if there has been any wrapping
// of any of the time units (year, month, etc) between the last checked
// time and now. If there has been, it does a collapsing of all records
// which need collapsing. This is the heart of the package. If this works
// correctly, Covid19 lockdowns are a pot of payasam.
//
func doWrap(l *HistLog) {

    // very critical check to see if time has moved backwards, as often
    // happens with system clock being reset or timezone changes. We work
    // with local time, which can move back and forth.
    if timeNow().Sub((*l).lastChecked) < 0 {

	// if time has indeed moved backwards, we will just do nothing in
	// each call to doWrap(), and bide our time till time once again
	// moves forward, i.e. current system time is "after" l.lastChecked
	log.Println("doWrap: INFO: now is before lastChecked")
	return
    }

    t := timeNow()
    lastyr, lastmth, lastdate := (*l).lastChecked.Date()
    var lastweek	int
    if !(*l).Day2Month {
	lastyr, lastweek = (*l).lastChecked.ISOWeek()
	lastdate = int((*l).lastChecked.Weekday())
    }
    lasthr, lastmin, _  := (*l).lastChecked.Clock()

    nowyr, nowmth, nowdate    := t.Date()
    var nowweek		int
    if !(*l).Day2Month {
	nowyr, nowweek = t.ISOWeek()
	nowdate = int(t.Weekday())
    }
    nowhr, nowmin, _     := t.Clock()

    var wraptype entryType_t

    if nowyr > lastyr {		// the year has wrapped since last time
	wraptype = etypeYear
    } else if (*l).Day2Month {
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
    log.Println("doWrap: DEBUG: wraptype =", wraptype)


    if wraptype >= etypeMinute {
	logEntryCollapse(l, etypeRaw, etypeMinute)
    }
    if wraptype >= etypeHour {
	logEntryCollapse(l, etypeMinute, etypeHour)
    }
    if wraptype >= etypeDay {
	logEntryCollapse(l, etypeHour, etypeDay)
    }
    if (*l).Day2Month {
	if wraptype >= etypeMonth {
	    logEntryCollapse(l, etypeDay, etypeMonth)
	    if wraptype >= etypeYear {
		logEntryCollapse(l, etypeMonth, etypeYear)
	    }
	}
    } else {
	if wraptype >= etypeWeek {
	    logEntryCollapse(l, etypeDay, etypeWeek)
	    if wraptype >= etypeYear {
		logEntryCollapse(l, etypeWeek, etypeYear)
	    }
	}
    }

    (*l).lastChecked = t
}  // end doWrap()

//
// Collapses all the log entries of a specific type and replaces
// them with a new entry of the next "fatter" type.
//
func logEntryCollapse(l *HistLog, wrapfrom, collapseto entryType_t) {
    var totalcount	Count_t
    var totalvals	Val_t
    var thisentry	histlogentry
    var lastyr, lastmth, lastweek, lastdate, lasthr, lastmin	int
    var newLogs		logList = make(logList, 0)

    {
	var acertainmonth time.Month
	lastyr, acertainmonth, lastdate = (*l).lastChecked.Date()
	lastmth = int(acertainmonth)
    }
    log.Println("logEntryCollapse: DEBUG: from", wrapfrom, "to", collapseto)

    if !(*l).Day2Month {
	lastyr, lastweek = (*l).lastChecked.ISOWeek()
	lastdate = int((*l).lastChecked.Weekday())
    }
    lasthr, lastmin, _  = (*l).lastChecked.Clock()

    // We aggregate all the lower-level entries...
    for _, thisentry = range (*l).Logs {
	if thisentry.T == wrapfrom {
	    totalcount += thisentry.Count
	    totalvals  += thisentry.Val
	} else {
	    //
	    // Here we are eliminating all the entries which we are
	    // collapsing, so that neLogs does not have them. We want
	    // newLogs to hold only those entries which we want to carry
	    // forward in the collapsed list.
	    //
	    newLogs = append(newLogs, thisentry)
	}
    }
    // ... and create a new entry
    thisentry.Count = totalcount
    thisentry.Val   = totalvals
    thisentry.T     = collapseto

    onelabel := entryLabel_t{elidxYR: int16(lastyr),
			elidxMTHWK: int16(lastweek),
			elidxDATE: int16(lastdate),
			elidxHR: int16(lasthr),
			elidxMIN: int16(lastmin)}
    thisentry.Elabel = onelabel
    if (*l).Day2Month {
	thisentry.Elabel[elidxMTHWK] = int16(lastmth)
    }
    newLogs = append(newLogs, thisentry)
    log.Printf("logEntryCollapse: DEBUG: old list =", len((*l).Logs),
		"new list =", len(newLogs))
    sort.Stable(newLogs)
    (*l).Logs = newLogs
} // end logEntryCollapse()

//
// internal function to compare two elements of logList. Useful for
// sort-related interface.
//
func (ll logList)Less(i, j int) bool {
    if int(ll[i].T) < int(ll[j].T) {
	return false	// a lower-level type, e.g. RAW, comes after a
			// higher level type, e.g. MIN or HR
    } else if int(ll[i].T) == int(ll[j].T) {
	//
	// When the types are the same, we start sorting based on
	// timestamps, which is what the Elabel array of Five Ints is
	//
	var labeli, labelj uint64

	labeli = uint64(ll[i].Elabel[elidxYR]) * 100000000 +
		    uint64(ll[i].Elabel[elidxMTHWK]) * 1000000 +
		    uint64(ll[i].Elabel[elidxDATE]) * 10000 +
		    uint64(ll[i].Elabel[elidxHR]) * 100 +
		    uint64(ll[i].Elabel[elidxMIN])
	labelj = uint64(ll[j].Elabel[elidxYR]) * 100000000 +
		    uint64(ll[j].Elabel[elidxMTHWK]) * 1000000 +
		    uint64(ll[j].Elabel[elidxDATE]) * 10000 +
		    uint64(ll[j].Elabel[elidxHR]) * 100 +
		    uint64(ll[j].Elabel[elidxMIN])
	log.Println("logList.Less: DEBUG: labeli =", labeli, "labelj =", labelj)
	return labeli < labelj		// i has an older timestamp
    } else {
	return true
    }
}

func (ll logList)Len() int {
    return len(ll)
}
//
// Internal function to swap two elements of a logList
//
func (ll logList)Swap(i, j int) {
    ll[i], ll[j] = ll[j], ll[i]
}

//
// Internal function to add a log entry to a HistLog instance. Called
// from the monitor goroutine.
//
func loginternal(l *HistLog, count Count_t, val Val_t) {
    var thisentry	histlogentry
    var now		time.Time = timeNow()
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
    onelabel := entryLabel_t{elidxYR: int16(nowyr),
			elidxMTHWK: int16(nowmth),
			elidxDATE: int16(nowdate),
			elidxHR: int16(nowhr),
			elidxMIN: int16(nowmin) }
    if !(*l).Day2Month {
	onelabel[elidxMTHWK] = int16(nowweek)
    }

    thisentry.T		= etypeRaw
    thisentry.Count	= count
    thisentry.Val	= val
    thisentry.Elabel	= onelabel

    (*l).Logs = append((*l).Logs, thisentry)
    (*l).pendingLogs++
} // end loginternal()

//
// Internal function to flush an instance to disk
//
func flushinternal(l *HistLog) {
    jsonHL, err := json.MarshalIndent(*l, "", "    ")
    if err != nil {
        log.Println("flushinternal: MarshalIndent:", err)
        return
    }
    if e:=ioutil.WriteFile((*l).storePath, jsonHL, 0660); e!= nil {
        log.Println("flushinternal: WriteFile:", e)
        return
    }
    (*l).pendingLogs = 0
    log.Println("flushinternal: DEBUG: flushed to", (*l).storePath)
    return
}

//
// scans the entries in a JSON-derived slice of Elabel data (which is
// essentially an array of five integers) and loads it into a
// entryLabel_t instance and returns it
//
func json2HLLElabel(elabeldata []interface{}) (*entryLabel_t) {
    var el entryLabel_t

    if len(elabeldata) > len(el) {
	return nil
    }
    for k, v := range elabeldata {
	if el[k] = int16(math.Round(v.(float64))); el[k] < 0 {
	    log.Println("json2HLLElabel: -ve label value: el[",k,"] =", el[k])
	    return nil
	}
    }
    log.Println("json2HLLElabel: DEBUG: returning [", el, "]")
    return &el
}

//
// scans entries in a JSON-derived slice of histlogentry data and loads
// this data into a slice of actual histlogentries and returns it
//
func json2HLlogs(logsslice []interface{}) (*logList) {
    var HLlogs	logList = make(logList, 0)
    var HLentry histlogentry

    for _, v := range logsslice {
	jsonentry := v.(map[string]interface{})
	for k2, v2 := range jsonentry {
	    switch strings.ToLower(k2) {
		case "elabel": {
		    if elabelptr:=json2HLLElabel(v2.([]interface{}));
			    elabelptr == nil {
			log.Println("json2HLlogs: could not get valid Elabel, exiting")
			return nil
		    } else {
			HLentry.Elabel = *elabelptr
		    }
		}
		case "type":
		    HLentry.T = entryType_t(int(math.Round(v2.(float64))))
		case "count":
		    HLentry.Count = Count_t(int(math.Round(v2.(float64))))
		case "val":
		    HLentry.Val = Val_t(int(math.Round(v2.(float64))))
		default:
		    log.Println("json2HLlogs: [",k2,"] is not valid field for histlogentry")
	    }
	}
	HLlogs = append(HLlogs, HLentry)
    }
    log.Println("json2HLlogs: DEBUG: returning Logs with",
		len(HLlogs),"elements")
    return &HLlogs
}

func json2HL(jsonblob *interface{}) (*HistLog, error) {
    var oneHL	HistLog

    onemap := (*jsonblob).(map[string]interface{})
    for k, v := range onemap {
	switch strings.ToLower(k) {
	    case "logs": {
		if oneHL.Logs = *(json2HLlogs(v.([]interface{})));
			oneHL.Logs == nil {
		    log.Println("json2HL: could not get valid Logs slice, exiting")
		    return nil, errors.New("No valid Logs[] found")
		}
	    }
	    case "day2month": {
		switch strings.ToLower(v.(string)) {
		    case "true": oneHL.Day2Month = true
		    case "false": oneHL.Day2Month = false
		    default: {
			log.Println("json2HL: value [", v.(string),
				"] not valid for Day2Month")
			return nil, errors.New("Invalid value: Day2Month")
		    }
		}
	    }
	    case "weekstart": {
		oneHL.Weekstart = time.Weekday(int(math.Round(v.(float64))))
	    }
	    case "agelimit": {
		oneHL.Agelimit = int(math.Round(v.(float64)))
	    }
	    case "autoflush": {
		oneHL.Autoflush = int(math.Round(v.(float64)))
	    }
	    case "club2mins": {
		switch strings.ToLower(v.(string)) {
		    case "true": oneHL.Club2mins = true
		    case "false": oneHL.Club2mins = false
		    default: {
			log.Println("json2HL: value [", v.(string),
				"] not valid for Club2mins")
			return nil, errors.New("Invalid value: Club2mins")
		    }
		}
	    }
	    default: {
		log.Println("json2HL: [",k,"] is not valid field for HistLog")
		// we will not exit, we will keep looping
	    }
	} // end switch
    } // end for

    oneHL.lastChecked		= timeNow()
    oneHL.pendingLogs		= 0
    oneHL.storePath		= ""
    oneHL.closed		= false
    oneHL.monitorexitat		= time.Time{}
    oneHL.tomonitor		= make(chan monmsg, MONITOR_CHANNEL_DEPTH)
    go monitor(&oneHL)

    log.Println("json2HL: DEBUG: returning with success")
    return &oneHL, nil
} // end json2HL()

//
// Internal function to sort the log entries of an instance in a
// human-readable sequence, oldest to newest
//
func (l *HistLog) sortinternal () {

}

//
// PUBLIC METHODS AND FUNCTIONS
//

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
    l.lastChecked	= timeNow()
    l.Logs		= make(logList, 0, 20)
    l.Day2Month		= d2m
    l.Weekstart		= weekstart
    l.Agelimit		= agelimit
    l.pendingLogs	= 0
    l.Autoflush		= af
    l.Club2mins		= club2mins
    l.closed		= false
    l.monitorexitat	= time.Time{}
    l.tomonitor		= make(chan monmsg, MONITOR_CHANNEL_DEPTH)

    go monitor(&l)
    return l
}

//
// Called to instantiate and load a HistLog from its backing store (a file);
// this file name is remembered for later Flush() calls
//
func Load(filename string) (*HistLog, error) {
    var oneHL		HistLog
    var filedata	[]byte
    var err		error

    filedata, err = ioutil.ReadFile(filename)
    if err != nil {
	log.Println("Load: ReadFile:", err)
	return nil, err
    }

    if e := json.Unmarshal(filedata, &oneHL); e != nil {
	log.Println("Load: Unmarshal:", err)
	return nil, e
    }

    // We now fill in the fields which are not loaded and stored, and
    // carry private content
    oneHL.lastChecked		= timeNow()
    oneHL.storePath		= filename
    oneHL.pendingLogs		= 0		// not needed, strictly
    oneHL.closed		= false
    oneHL.monitorexitat		= time.Time{}
    oneHL.tomonitor		= make(chan monmsg, MONITOR_CHANNEL_DEPTH)

    go monitor(&oneHL)

    log.Println("Load: DEBUG: loaded from", filename,
		"and returning with success")
    return &oneHL, nil
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
func Read(r io.Reader) (*HistLog, error) {
    var oneHL	*HistLog
    var e	error

    dec := json.NewDecoder(r)
    var jsonblob	interface{}	// this means "it holds anything"

    for {
	if e := dec.Decode(&jsonblob); e == io.EOF {
	    break
	} else if e != nil {
	    log.Println("Read: Decoding failed:", e)
	    return nil, e
	}
	oneHL, e = json2HL(&jsonblob)
    }
    log.Println("Read: DEBUG: returning with success")
    return oneHL, e
}


//
// Exported method to add a new entry to a HistLog instance.
//
func (l *HistLog) Log(c Count_t, v Val_t) (error) {
    if (*l).closed {
	return errors.New("HistLog object is closed for business")
    }
    var msg monmsg

    msg.mtype	= mTypeLog
    msg.count	= c
    msg.val		= v
    msg.strparam	= nil

    (*l).tomonitor <-msg
    return nil
}

//
// Exported method to add a new entry to a HistLog instance with just an
// increment of the count
//
func (l *HistLog) Incr(c Count_t) (error) {
    if (*l).closed {
	return errors.New("HistLog object is closed for business")
    }
    var msg monmsg

    msg.mtype	= mTypeIncr
    msg.count	= c
    msg.val		= Val_t(0)
    msg.strparam	= nil

    (*l).tomonitor <-msg
    return nil
}

//
// Set the flush-to filename.
//
func (l *HistLog) SetFlushFile(flushtofilename string) (error) {
    if (*l).closed {
	return errors.New("HistLog object is closed for business")
    }
    var somebytes = []byte{'a','b','c'}

    // do a trial write to the filename given
    if e := ioutil.WriteFile(flushtofilename, somebytes, 0660); e != nil {
	log.Println("SetFlushFile: WriteFile test for", flushtofilename,
			"failed")
	return e
    }
    if e := os.Remove(flushtofilename); e != nil {
	log.Println("SetFlushFile: file deletion for", flushtofilename,
			"failed")
	return e
    }
    var msg monmsg

    msg.mtype		= mTypeSetFlushFile
    msg.count		= Count_t(0)
    msg.val		= Val_t(0)
    msg.strparam	= &flushtofilename

    (*l).tomonitor <-msg
    return nil
}

//
// Do an explicit flush of the HistLog to its pre-set flush-to file
//
func (l *HistLog) Flush() (err error) {
    if (*l).closed {
	return errors.New("HistLog object is closed for business")
    }
    if (*l).storePath == "" {
	return errors.New("flush-to filename not set")
    }
    var msg	monmsg

    msg.mtype		= mTypeFlush
    msg.count		= Count_t(0)
    msg.val		= Val_t(0)
    msg.strparam	= nil

    (*l).tomonitor<- msg
    return nil
}

//
// Write out a nicely formatted representation of the HistLog object to
// the given writable stream.
//
func (l *HistLog) Write(w io.Writer) (err error) {
    if (*l).closed {
	return errors.New("HistLog object is closed for business")
    }
    var msg		monmsg
    var fetcheddata	string
    var c		int
    const C_MAX		int = 10	// prevent infinite loops

    msg.mtype		= mTypeGet
    msg.count		= Count_t(0)
    msg.val		= Val_t(0)
    msg.strparam	= &fetcheddata

    (*l).tomonitor<- msg
    //
    // We go into a loop of sleeping and waiting, till the monitor
    // goroutine gets us the data we are waiting for and loads it
    // into the string which we've passed by reference.
    //
    for c=0; (len(*msg.strparam) == 0) && (c<C_MAX); c++ {
	time.Sleep(time.Duration(datafetch_SLEEP_SEC) * time.Second)
    }
    if c ==  C_MAX {
	log.Println("Write: data-fetch loop seems stuck forever")
	return errors.New("data-fetch loop timed out")
    }
    time.Sleep(time.Duration(datafetch_SLEEP_SEC) * time.Second) // for good measure

    _, err = w.Write([]byte(*msg.strparam))
    log.Println("Write: DEBUG: done, err =",err)
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
func (l *HistLog) Close() {
    if (*l).closed {
	return
    }
    var msg		monmsg

    msg.mtype		= mTypeClose
    msg.count		= Count_t(0)
    msg.val		= Val_t(0)
    msg.strparam	= nil

    l.tomonitor<- msg
    log.Println("Close: DEBUG: done")
} // end Close(xxx)

//
// Called to pull out a human readable string version of the complete set
// of log entries.
//
func (l *HistLog) Strings() (s string) {
    return s
}



//
// THE FOLLOWING VARIABLES AND FUNCTIONS ARE USEFUL FOR REGRESSION
// TESTING OF THE PACKAGE. NOT USEFUL FOR PRODUCTION USE.
//

//
// this variable decides how long is the interval between loops of the
// monitor goroutines. In production, a minute is a good value. For
// testing, shorter may be needed.
//
var monitorLoopDelay	time.Duration	= time.Minute
func SetMonitorLoopDelay(d time.Duration) {
    monitorLoopDelay = d
}
//
// Parameterise the time.Now() operation by making it a function
// variable, so that we can replace time.Now() with some other function
// for testing purposes.
//
var timeNow func() time.Time = time.Now
//
// Can be called with the name of a function which will return the
// "current" time, and will set this function in place of time.Now() for
// the purposes of this package. One of the popular functions which can
// be supplied to it is TimeNowEveryStep, with a step of time.Second or
// time.Minute, so that the current time will start stepping in those
// steps henceforth.
//
func SetTimeNow(tf func() time.Time) {
    timeNow = tf
}
//
// May be called with a step size (time.Duration) parameter, and will 
// return a closure which will step by the given step size and return 
// a new time every time it is called.
//
// The output of this function may be passed as input parameter to
// SetTimeNow()
//
func TimeNowEveryStep(step time.Duration) func() time.Time {
    var  timecounter time.Time = time.Now()
    return func() time.Time {
	timecounter = timecounter.Add(step)
	return timecounter
    }
}
//
// Called with a slice of step sizes, and will step through this slice
// and increment "current" time by each step size in turn, looping back
// when the slice finishes. Returns a function which may be passed as
// input parameter to SetTimeNow().
//
func TimeNowStepSlice (steps []int) func() time.Time {
    var timecounter time.Time = time.Now()
    var i int = 0
    return func() time.Time {
	timecounter = timecounter.Add(time.Duration(int64(time.Second) *
					int64(steps[i])))
	i++
	if i == len(steps) { i = 0 }
	return timecounter
    }
}
