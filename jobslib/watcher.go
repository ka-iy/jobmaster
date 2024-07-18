package jobslib

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// WaitForFile waits until a file appears on the filesystem.
// To ensure that it is not a busy-wait/spinlock, it sleeps for a tiny bit between checks.
func (j *JobsLib) WaitForFile(ctx context.Context, logfile string) error {
	// TODO: Make these configurable
	sleepMillis := 5
	numSleepCycles := 200
	i := 0

	for {
		select {
		case <-ctx.Done():
			log.Printf("WaitForFile (%s): context is done, stop waiting for file", logfile)
			return ctx.Err()
		default:
		}
		i++
		if i >= numSleepCycles {
			return fmt.Errorf("WaitForFile (%s): Did not find logfile after %d cycles of %d millisecs each", logfile, numSleepCycles, sleepMillis)
		}
		_, err := os.Stat(logfile)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				time.Sleep(time.Duration(sleepMillis) * time.Millisecond)
			} else {
				// Other error, bail
				return err
			}
		} else {
			// Continue on with life
			return nil
		}
	}
}

// Watcher watches the given file for modification events. When an event is detected, any readers
// of the watched file are notified via a condition variable broadcast. Watcher itself listens for
// a message from the file writer (of which there will/should only ever be a single one) on ctx,
// the cancellation of which will terminate the watch.
func (j *JobsLib) Watcher(ctx context.Context, logfile string) error {
	err := j.WaitForFile(ctx, logfile)
	if err != nil {
		log.Printf("WATCHER (%s): ERROR: file check error, bye bye: %v", logfile, err)
		return err
	}

	// Set up the inotify infrastructure
	ifd, err := syscall.InotifyInit()
	if err != nil {
		log.Printf("WATCHER (%s): ERROR: InotifyInit, bye bye: %v", logfile, err)
		return err
	}
	defer syscall.Close(ifd)

	iwd, err := syscall.InotifyAddWatch(ifd, logfile, syscall.IN_MODIFY|syscall.IN_CLOSE_WRITE)
	if err != nil {
		log.Printf("WATCHER (%s): ERROR: InotifyAddWatch, bye bye: %v", logfile, err)
		return err
	}

	// Take our very own mutex lock.
	j.WatcherLock.Lock()
	defer func() {
		log.Printf("WATCHER (%s): bye bye: Removing inotify watch, unlocking, broadcasting", logfile)
		j.WatcherLock.Unlock()
		j.CvEvent.Broadcast() // To give any readers a final wakeup
		s, err := syscall.InotifyRmWatch(ifd, uint32(iwd))
		if s != 0 || err != nil {
			log.Printf("WATCHER (%s): bye bye ERROR: Failed InotifyRmWatch", logfile)
		}
	}()

	// These many inotify events (or less) will be read into a buffer.
	// TODO: Make this configurable.
	numInotiFyEventsToBuffer := 100

	log.Printf("WATCHER (%s): File monitoring begun.", logfile)

	// inotify events are read into this buffer.
	ebuf := make([]byte, syscall.SizeofInotifyEvent*numInotiFyEventsToBuffer)
	for {

		remainingEvents := 0

		select {
		case <-ctx.Done():
			log.Printf("WATCHER (%s): context is done, my watch has ended, bye bye", logfile)
			return nil // NOTE: We do not return ctx.Err() here

		default:
		}

		// Read the inotify file desciptor for inotify events under the condvar lock.
		j.CvEvent.L.Lock()
		bytesread, err := syscall.Read(ifd, ebuf)
		j.CvEvent.L.Unlock()
		if err != nil {
			log.Printf("WATCHER (%s): WARNING: failure reading inotify event, will continue: %v", logfile, err)
			//return err
			continue
		}

		if bytesread == 0 {
			log.Printf("WATCHER (%s): 0 bytes read, watch has ended, bye bye", logfile)
			return nil
		}

		// Partial read - the bane of filesystems.
		if bytesread < syscall.SizeofInotifyEvent {
			continue
		}

		// Process the events in the buffer.
		var offset uint32
		for offset <= uint32(bytesread-syscall.SizeofInotifyEvent) {
			j.CvEvent.Broadcast() // Let any readers know about it

			// Contortions to figure out the event. La vita no e bella :-(
			raw := (*syscall.InotifyEvent)(unsafe.Pointer(&ebuf[offset])) // gulp! Unsafe!
			if (raw.Mask & syscall.IN_CLOSE_WRITE) == syscall.IN_CLOSE_WRITE {
				// Send broadcasts for as many events as are remaining.
				// Note that inotify _still_ has issues with missing events :-(
				// So we'll send a few extra broadcasts just to be sure.
				for i := 0; i <= (remainingEvents + 10); i++ {
					j.CvEvent.Broadcast() // Let any readers know about it
				}

				// We're done
				log.Printf("WATCHER (%s): IN_CLOSE_WRITE detected, watch has ended, bye bye", logfile)
				return nil
			}

			// Move to the next event
			offset += (syscall.SizeofInotifyEvent + raw.Len)
			remainingEvents++
		}
	}
}
