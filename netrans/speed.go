package netrans

import (
	"io"
	"sync"
	"sync/atomic"
)

// SpeedTrackingReadWriter 包装一个 io.ReadWriter 以跟踪上传和下载速度。
type SpeedTrackingReadWriterCloser struct {
	rwc io.ReadWriteCloser // 底层的 ReadWriter

	totalUploadedBytes   atomic.Uint64
	totalDownloadedBytes atomic.Uint64

	mu                   sync.Mutex
	lastUploadSnapshot   uint64 // 上次检查时的总上传字节数
	lastDownloadSnapshot uint64 // 上次检查时的总下载字节数
}

func NewSpeedTrackingReadWriteCloser(rwc io.ReadWriteCloser) *SpeedTrackingReadWriterCloser {
	return &SpeedTrackingReadWriterCloser{
		rwc: rwc,
	}
}

func (s *SpeedTrackingReadWriterCloser) Read(p []byte) (n int, err error) {
	n, err = s.rwc.Read(p)
	if n > 0 {
		s.totalDownloadedBytes.Add(uint64(n))
	}
	return
}

func (s *SpeedTrackingReadWriterCloser) Write(p []byte) (n int, err error) {
	n, err = s.rwc.Write(p)
	if n > 0 {
		s.totalUploadedBytes.Add(uint64(n))
	}
	return
}

func (s *SpeedTrackingReadWriterCloser) Close() error {
	return s.rwc.Close()
}

func (s *SpeedTrackingReadWriterCloser) GetSpeedInterval() (bytesUploadedInterval, bytesDownloadedInterval uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	currentUploaded := s.totalUploadedBytes.Load()
	currentDownloaded := s.totalDownloadedBytes.Load()

	// 自上次检查以来传输的字节数
	bytesUploadedInterval = currentUploaded - s.lastUploadSnapshot
	bytesDownloadedInterval = currentDownloaded - s.lastDownloadSnapshot

	s.lastUploadSnapshot = currentUploaded
	s.lastDownloadSnapshot = currentDownloaded
	return
}

type SpeedCaculator struct {
	totalUploadedBytes   atomic.Uint64
	totalDownloadedBytes atomic.Uint64

	mu                   sync.Mutex
	lastUploadSnapshot   uint64 // 上次检查时的总上传字节数
	lastDownloadSnapshot uint64 // 上次检查时的总下载字节数
}

func NewSpeedCaculator() *SpeedCaculator {
	return &SpeedCaculator{}
}

func (s *SpeedCaculator) AddSpeed(upload, download uint64) {
	s.totalUploadedBytes.Add(upload)
	s.totalDownloadedBytes.Add(download)
}

func (s *SpeedCaculator) Statistic() (uint64, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	currentUploaded := s.totalUploadedBytes.Load()
	currentDownloaded := s.totalDownloadedBytes.Load()

	// 自上次检查以来传输的字节数
	bytesUploadedInterval := currentUploaded - s.lastUploadSnapshot
	bytesDownloadedInterval := currentDownloaded - s.lastDownloadSnapshot

	s.lastUploadSnapshot = currentUploaded
	s.lastDownloadSnapshot = currentDownloaded

	return bytesUploadedInterval, bytesDownloadedInterval
}
