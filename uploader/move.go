package uploader

import (
	"fmt"
	"github.com/l3uddz/crop/cache"
	"github.com/l3uddz/crop/pathutils"
	"github.com/l3uddz/crop/rclone"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"time"
)

type MoveInstruction struct {
	From       string
	To         string
	ServerSide bool
}

func (u *Uploader) Move(serverSide bool, additionalRcloneParams []string) error {
	var moveRemotes []MoveInstruction

	// create move instructions
	if serverSide {
		// this is a server side move
		for _, remote := range u.Config.Remotes.MoveServerSide {
			moveRemotes = append(moveRemotes, MoveInstruction{
				From:       remote.From,
				To:         remote.To,
				ServerSide: true,
			})
		}
	} else {
		// this is a normal move (to only one location)
		moveRemotes = append(moveRemotes, MoveInstruction{
			From:       u.Config.LocalFolder,
			To:         u.Config.Remotes.Move,
			ServerSide: false,
		})
	}

	// iterate all remotes and run copy
	for _, move := range moveRemotes {
		// set variables
		attempts := 1
		rLog := u.Log.WithFields(logrus.Fields{
			"move_to":   move.To,
			"move_from": move.From,
			"attempts":  attempts,
		})

		// move to remote
		for {
			// get service account file
			var serviceAccount *pathutils.Path
			var err error

			if u.ServiceAccountCount > 0 && !serverSide {
				// server side moves not supported with service account files
				serviceAccount, err = u.getAvailableServiceAccount()
				if err != nil {
					return errors.WithMessagef(err,
						"aborting further move attempts of %q due to serviceAccount exhaustion",
						move.From)
				}

				// reset log
				rLog = u.Log.WithFields(logrus.Fields{
					"move_to":         move.To,
					"move_from":       move.From,
					"attempts":        attempts,
					"service_account": serviceAccount.RealPath,
				})
			}

			// move
			rLog.Info("Moving...")
			success, exitCode, err := rclone.Move(u.Config, move.From, move.To, serviceAccount, serverSide,
				additionalRcloneParams)

			// check result
			if err != nil {
				rLog.WithError(err).Errorf("Failed unexpectedly...")
				return errors.WithMessagef(err, "move failed unexpectedly with exit code: %v", exitCode)
			} else if success {
				// successful exit code
				break
			} else if serverSide {
				// server side moves not supported with service accounts
				return fmt.Errorf("failed and cannot proceed with exit code: %v", exitCode)
			}

			// are we using service accounts?
			if u.ServiceAccountCount == 0 {
				return fmt.Errorf("move failed with exit code: %v", exitCode)
			}

			// is this an exit code we can retry?
			switch exitCode {
			case rclone.EXIT_FATAL_ERROR:
				// ban this service account
				if err := cache.Set(serviceAccount.RealPath, time.Now().UTC().Add(25*time.Hour)); err != nil {
					rLog.WithError(err).Error("Failed banning service account, cannot try again...")
					return fmt.Errorf("failed banning service account: %v", serviceAccount.RealPath)
				}

				// attempt copy again
				rLog.Warnf("Move failed with retryable exit code %v, trying again...", exitCode)
				attempts++
				continue
			default:
				return fmt.Errorf("failed and cannot proceed with exit code: %v", exitCode)
			}
		}
	}

	return nil
}