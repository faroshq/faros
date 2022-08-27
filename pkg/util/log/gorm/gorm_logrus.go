package gorm

import (
	"context"
	"errors"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/utils"
)

type logger struct {
	log                   *logrus.Entry
	SlowThreshold         time.Duration
	SourceField           string
	SkipErrRecordNotFound bool
}

func New(log *logrus.Entry) *logger {
	return &logger{
		log:                   log,
		SkipErrRecordNotFound: false,
	}
}

func (l *logger) LogMode(gormlogger.LogLevel) gormlogger.Interface {
	return l
}

func (l *logger) Info(ctx context.Context, s string, args ...interface{}) {
	l.log.WithContext(ctx).Infof(s, args)
}

func (l *logger) Warn(ctx context.Context, s string, args ...interface{}) {
	l.log.WithContext(ctx).Warnf(s, args)
}

func (l *logger) Error(ctx context.Context, s string, args ...interface{}) {
	l.log.WithContext(ctx).Errorf(s, args)
}

func (l *logger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	elapsed := time.Since(begin)
	log := l.log

	sql, _ := fc()
	if l.SourceField != "" {
		log = log.WithFields(logrus.Fields{l.SourceField: utils.FileWithLineNum()})
	}
	if err != nil && !(errors.Is(err, gorm.ErrRecordNotFound) && l.SkipErrRecordNotFound) {
		log = log.WithFields(logrus.Fields{"error": err})
		log.WithContext(ctx).Errorf("%s [%s]", sql, elapsed)
		return
	}

	if l.SlowThreshold != 0 && elapsed > l.SlowThreshold {
		log.WithContext(ctx).Warnf("%s [%s]", sql, elapsed)
		return
	}

	log.WithContext(ctx).Debugf("%s [%s]", sql, elapsed)

}
