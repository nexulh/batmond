package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/0xAX/notificator"
	"github.com/distatus/battery"
	"github.com/nightlyone/lockfile"
)

var (
	minDelay        int64
	verbose         bool
	critPercentage  int
	critMinutesLeft int
	iconSize        int
)

func init() {
	flag.IntVar(&critPercentage, "critPercentage", 5, "critical notifications below this battery percentage")
	flag.IntVar(&critMinutesLeft, "critMinutesLeft", 15, "critical notifications when less than X minutes left")
	flag.Int64Var(&minDelay, "delay", 120, "minimum delay (in seconds) between notifications")
	flag.BoolVar(&verbose, "verbose", false, "verbose output")
	flag.IntVar(&iconSize, "iconSize", 48, "icon size")
	flag.Parse()
}

func main() {
	lfiledir := filepath.Join(os.Getenv("HOME"), ".batmond")
	if err := os.MkdirAll(lfiledir, os.ModePerm); err != nil {
		fmt.Printf("Could not create app-directory: %s\n", err)
	}

	lfilepath := filepath.Join(lfiledir, ".lock")
	lfile, err := lockfile.New(lfilepath)
	if err != nil {
		fmt.Printf("Could not initialize lockfile: %s\n", err)
		return
	}
	if err := lfile.TryLock(); err != nil {
		fmt.Printf("Could not aquire lockfile (%s): %s\n", lfilepath, err)
		return
	}
	defer lfile.Unlock()

	intSig := make(chan os.Signal)

	vPrintf("main: Battery Monitor running\n")

	nn := NotificationNotifier{
		notifier: notificator.New(notificator.Options{
			DefaultIcon: fmt.Sprintf(".batmond/battery_%d.jpg", iconSize),
			AppName:     "Battery Monitor",
		}),
	}

	bm := BatteryMonitor{
		notificationDelay: time.Second * time.Duration(minDelay),
		notifier:          nn,
	}

	signal.Notify(intSig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	running := true

	bm.Update()
	for running {
		select {
		case <-intSig:
			running = false
		case <-time.Tick(time.Second * 5):
			bm.Update()
		}
	}
}

type BatteryMonitor struct {
	lastNotification  time.Time
	notificationDelay time.Duration
	lastBatteryState  *battery.Battery

	notifier Notifier
}

func (bm *BatteryMonitor) Update() bool {
	batteries, err := battery.GetAll()
	if err != nil {

		//vPrintf("BatteryMonitor.Update: battery.GetAll: : %v\n", err)
	}

	if len(batteries) < 1 {
		vPrintf("BatteryMonitor.Update: No batteries found\n")
		return false
	}

	for _, b := range batteries {
		if bm.shouldReset(*b) {
			bm.setBatteryState(*b)
			bm.notify(*b)
		} else if bm.shouldNotify(*b) {
			bm.notify(*b)
		}
	}
	return true
}

func (bm *BatteryMonitor) shouldReset(b battery.Battery) bool {
	if bm.lastBatteryState == nil {
		vPrintf("BatteryMonitor.shouldReset: No previous state found => true\n")
		return true
	}

	return false
}

func (bm *BatteryMonitor) shouldNotify(b battery.Battery) bool {
	currentPercentage := b.Current / b.Full
	minsLeft := int((b.Current * 60) / b.ChargeRate)

	// Ignore invalid charge numbers
	if currentPercentage > 1.0 || currentPercentage < 0.0 {
		return false
	}

	if bm.lastBatteryState == nil {
		return true
	}
	oldPercentage := bm.lastBatteryState.Current / bm.lastBatteryState.Full

	// New state? where state = Discharging/Charging and so on
	if bm.isNewState(b) {
		vPrintf("BatteryMonitor.shouldNotify: new state => true\n")
		return true
	}

	if b.State != battery.Discharging {
		return false
	}

	if b.Current > bm.lastBatteryState.Current {
		vPrintf("BatteryMonitor.shouldNotify: charge is higher than last => true\n")
		return true
	}

	if time.Now().After(bm.lastNotification.Add(bm.notificationDelay)) {
		if b.State == battery.Discharging && currentPercentage < oldPercentage*0.5 {
			vPrintf("BatteryMonitor.shouldNotify: half of previous notification charge => true\n")
			return true
		}
		if currentPercentage < float64(critPercentage/100.0) {
			vPrintf("BatteryMonitor.shouldNotify: less than %d%% left => true\n", critPercentage)
			return true
		}
		if minsLeft < critMinutesLeft {
			vPrintf("BatteryMonitor.shouldNotify: less than %d minutes left => true\n", critMinutesLeft)
			return true
		}

	}

	return false
}

func (bm *BatteryMonitor) notify(b battery.Battery) {
	currentPercentage := b.Current / b.Full
	var minsLeft int
	var timeLeft string

	if b.State == battery.Discharging {
		minsLeft = int((b.Current * 60) / b.ChargeRate)
	} else if b.State == battery.Charging {
		minsLeft = int(((b.Full - b.Current) * 60) / b.ChargeRate)
	}

	if minsLeft > 60 {
		timeLeft = fmt.Sprintf("%d hour(s), %d minute(s)", minsLeft/60, minsLeft%60)
	} else {
		timeLeft = fmt.Sprintf("%d minute(s)", minsLeft)

	}

	msg := fmt.Sprintf("%s at %.0f%%\n%s left", b.State, (currentPercentage * 100), timeLeft)

	if b.State == battery.Discharging && ((currentPercentage*100) < float64(critPercentage) ||
		(minsLeft < critMinutesLeft)) {
		bm.notifier.Critical(msg)
	} else {
		bm.notifier.Print(msg)
	}

	bm.setBatteryState(b)
	bm.lastNotification = time.Now()
}

func (bm *BatteryMonitor) setBatteryState(b battery.Battery) {
	bm.lastBatteryState = &b
}

func (bm *BatteryMonitor) isNewState(b battery.Battery) bool {
	if bm.lastBatteryState == nil {
		return true
	}
	return b.State != bm.lastBatteryState.State
}

type Notifier interface {
	Print(s string)
	Critical(s string)
}

type NotificationNotifier struct {
	notifier *notificator.Notificator
}

func (nf NotificationNotifier) _print(s string, critical bool) {
	if nf.notifier == nil {
		fmt.Println("NotificationNotifier: NIL notifier")
		return
	}

	msgLevel := notificator.UR_NORMAL
	if critical == true {
		msgLevel = notificator.UR_CRITICAL
	}

	nf.notifier.Push("Battery", fmt.Sprintf("%s", s), "", msgLevel)
}

func (nf NotificationNotifier) Print(s string) {
	nf._print(s, false)
}

func (nf NotificationNotifier) Critical(s string) {
	nf._print(s, true)
}

func vPrintf(format string, a ...interface{}) {
	if verbose {
		log.Printf(format, a...)
		//fmt.Printf(format, a...)
	}
}
