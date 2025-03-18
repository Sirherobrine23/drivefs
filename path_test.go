package drivefs

import "testing"

func TestPath(t *testing.T) {
	m := pathManipulate("/google/test/23/")
	if len(m.SplitPath()) != 3 {
		t.Errorf("invalid Path spliter: %+v != %+v", m.CleanPath(), [][2]string{{"google", "google"}, {"google/test", "test"}, {"google/test/23", "23"}})
		t.FailNow()
	}
	if m.CleanPath() != "google/test/23" {
		t.Errorf("invalid Path fixer: %s != %s", m.CleanPath(), "google/test/23")
		t.FailNow()
	}
	if !m.IsSubFolder() {
		t.Errorf("invalid subfolder detect: %v != %v", true, m.IsSubFolder())
		t.FailNow()
	}

	m = pathManipulate("\\google\\test\\23\\")
	if len(m.SplitPath()) != 3 {
		t.Errorf("invalid Path spliter: %+v != %+v", m.CleanPath(), [][2]string{{"google", "google"}, {"google/test", "test"}, {"google/test/23", "23"}})
		t.FailNow()
	}
	if m.CleanPath() != "google/test/23" {
		t.Errorf("invalid Path fixer: %s != %s", m.CleanPath(), "google/test/23")
		t.FailNow()
	}
	if !m.IsSubFolder() {
		t.Errorf("invalid subfolder detect: %v != %v", true, m.IsSubFolder())
		t.FailNow()
	}

	m = pathManipulate("\\\\google\\\\test\\\\23\\\\")
	if len(m.SplitPath()) != 3 {
		t.Errorf("invalid Path spliter: %+v != %+v", m.CleanPath(), [][2]string{{"google", "google"}, {"google/test", "test"}, {"google/test/23", "23"}})
		t.FailNow()
	}
	if m.CleanPath() != "google/test/23" {
		t.Errorf("invalid Path fixer: %s != %s", m.CleanPath(), "google/test/23")
		t.FailNow()
	}
	if !m.IsSubFolder() {
		t.Errorf("invalid subfolder detect: %v != %v", true, m.IsSubFolder())
		t.FailNow()
	}
}
