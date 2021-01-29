package enforcer

import (
	"io/ioutil"
	"os"
	"strings"
	"sync"

	kl "github.com/accuknox/KubeArmor/KubeArmor/common"
	kg "github.com/accuknox/KubeArmor/KubeArmor/log"
	tp "github.com/accuknox/KubeArmor/KubeArmor/types"
)

// ======================= //
// == AppArmor Enforcer == //
// ======================= //

// AppArmorEnforcer Structure
type AppArmorEnforcer struct {
	AppArmorProfiles     map[string]int
	AppArmorProfilesLock *sync.Mutex
}

// NewAppArmorEnforcer Function
func NewAppArmorEnforcer() *AppArmorEnforcer {
	ae := &AppArmorEnforcer{}

	ae.AppArmorProfiles = map[string]int{}
	ae.AppArmorProfilesLock = &sync.Mutex{}

	if !kl.IsK8sLocal() {
		// mount securityfs
		kl.GetCommandOutputWithoutErr("mount", []string{"-t", "securityfs", "securityfs", "/sys/kernel/security"})
	}

	files, err := ioutil.ReadDir("/etc/apparmor.d")
	if err != nil {
		kg.Errf("Failed to read /etc/apparmor.d (%s)", err.Error())
		return nil
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		fileName := file.Name()

		data, err := ioutil.ReadFile("/etc/apparmor.d/" + fileName)
		if err != nil {
			kg.Errf("Failed to read /etc/apparmor.d/%s (%s)", fileName, err.Error())
			return nil
		}

		str := string(data)

		if strings.Contains(str, "KubeArmor") {
			if _, err := kl.GetCommandOutputWithErr("apparmor_parser", []string{"-R", "/etc/apparmor.d/" + fileName}); err != nil {
				kg.Errf("Failed to detach /etc/apparmor.d/%s (%s)", fileName, err.Error())
				return nil
			}

			if err := os.Remove("/etc/apparmor.d/" + fileName); err != nil {
				kg.Errf("Failed to remove /etc/apparmor.d/%s (%s)", fileName, err.Error())
				return nil
			}
		}
	}

	return ae
}

// DestroyAppArmorEnforcer Function
func (ae *AppArmorEnforcer) DestroyAppArmorEnforcer() error {
	for profileName := range ae.AppArmorProfiles {
		ae.UnregisterAppArmorProfile(profileName)
	}

	return nil
}

// ================================= //
// == AppArmor Profile Management == //
// ================================= //

// RegisterAppArmorProfile Function
func (ae *AppArmorEnforcer) RegisterAppArmorProfile(profileName string) bool {
	apparmorDefault := "## == Managed by KubeArmor == ##\n" +
		"\n" +
		"#include <tunables/global>\n" +
		"\n" +
		"profile apparmor-default flags=(attach_disconnected,mediate_deleted) {\n" +
		"  ## == PRE START == ##\n" +
		"  #include <abstractions/base>\n" +
		"  umount,\n" +
		"  file,\n" +
		"  network,\n" +
		"  capability,\n" +
		"  ## == PRE END == ##\n" +
		"\n" +
		"  ## == POLICY START == ##\n" +
		"  ## == POLICY END == ##\n" +
		"\n" +
		"  ## == POST START == ##\n" +
		"  deny @{PROC}/{*,**^[0-9*],sys/kernel/shm*} wkx,\n" +
		"  deny @{PROC}/sysrq-trigger rwklx,\n" +
		"  deny @{PROC}/mem rwklx,\n" +
		"  deny @{PROC}/kmem rwklx,\n" +
		"  deny @{PROC}/kcore rwklx,\n" +
		"\n" +
		"  deny mount,\n" +
		"\n" +
		"  deny /sys/[^f]*/** wklx,\n" +
		"  deny /sys/f[^s]*/** wklx,\n" +
		"  deny /sys/fs/[^c]*/** wklx,\n" +
		"  deny /sys/fs/c[^g]*/** wklx,\n" +
		"  deny /sys/fs/cg[^r]*/** wklx,\n" +
		"  deny /sys/firmware/efi/efivars/** rwklx,\n" +
		"  deny /sys/kernel/security/** rwklx,\n" +
		"  ## == POST END == ##\n" +
		"}\n"

	ae.AppArmorProfilesLock.Lock()
	defer ae.AppArmorProfilesLock.Unlock()

	if _, err := os.Stat("/etc/apparmor.d/" + profileName); err == nil {
		content, err := ioutil.ReadFile("/etc/apparmor.d/" + profileName)
		if err != nil {
			kg.Printf("Unabale to register an AppArmor profile (%s, %s)", profileName, err.Error())
			return false
		}

		if !strings.Contains(string(content), "KubeArmor") {
			kg.Printf("Unabale to register an AppArmor profile (%s) (out-of-control)", profileName)
			return false
		}
	} else {
		newProfile := strings.Replace(apparmorDefault, "apparmor-default", profileName, -1)

		newFile, err := os.Create("/etc/apparmor.d/" + profileName)
		if err != nil {
			kg.Err(err.Error())
			return false
		}
		defer newFile.Close()

		if _, err := newFile.WriteString(newProfile); err != nil {
			kg.Err(err.Error())
			return false
		}
	}

	if _, err := kl.GetCommandOutputWithErr("apparmor_parser", []string{"-r", "-W", "/etc/apparmor.d/" + profileName}); err == nil {
		if _, ok := ae.AppArmorProfiles[profileName]; !ok {
			ae.AppArmorProfiles[profileName] = 1
			kg.Printf("Registered an AppArmor profile (%s)", profileName)
		} else {
			ae.AppArmorProfiles[profileName]++
			kg.Printf("Increased the refCount (%d -> %d) of an AppArmor profile (%s)", ae.AppArmorProfiles[profileName]-1, ae.AppArmorProfiles[profileName], profileName)
		}
	} else {
		kg.Printf("Failed to register an AppArmor profile (%s, %s)", profileName, err.Error())
		return false
	}

	return true
}

// UnregisterAppArmorProfile Function
func (ae *AppArmorEnforcer) UnregisterAppArmorProfile(profileName string) bool {
	ae.AppArmorProfilesLock.Lock()
	defer ae.AppArmorProfilesLock.Unlock()

	if _, err := os.Stat("/etc/apparmor.d/" + profileName); err == nil {
		content, err := ioutil.ReadFile("/etc/apparmor.d/" + profileName)
		if err != nil {
			kg.Printf("Unabale to unregister an AppArmor profile (%s, %s)", profileName, err.Error())
			return false
		}

		if !strings.Contains(string(content), "KubeArmor") {
			kg.Printf("Unabale to unregister an AppArmor profile (%s) (out-of-control)", profileName)
			return false
		}
	}

	if referenceCount, ok := ae.AppArmorProfiles[profileName]; ok {
		if referenceCount > 1 {
			ae.AppArmorProfiles[profileName]--
			kg.Printf("Decreased the refCount (%d -> %d) of an AppArmor profile (%s)", ae.AppArmorProfiles[profileName]+1, ae.AppArmorProfiles[profileName], profileName)
		} else {
			if _, err := kl.GetCommandOutputWithErr("apparmor_parser", []string{"-R", "/etc/apparmor.d/" + profileName}); err != nil {
				kg.Printf("Failed to unregister an AppArmor profile (%s, %s)", profileName, err.Error())
				return false
			}

			if err := os.Remove("/etc/apparmor.d/" + profileName); err != nil {
				kg.Err(err.Error())
				return false
			}

			delete(ae.AppArmorProfiles, profileName)
			kg.Printf("Unregistered an AppArmor profile (%s)", profileName)
		}
	} else {
		kg.Printf("Failed to unregister an unknown AppArmor profile (%s)", profileName)
		return false
	}

	return true
}

// ================================= //
// == Security Policy Enforcement == //
// ================================= //

// UpdateAppArmorProfile Function
func UpdateAppArmorProfile(conGroup tp.ContainerGroup, appArmorProfile string, securityPolicies []tp.SecurityPolicy) {
	if policyCount, newProfile, ok := GenerateAppArmorProfile(appArmorProfile, securityPolicies); ok {
		newfile, err := os.Create("/etc/apparmor.d/" + appArmorProfile)
		if err != nil {
			kg.Err(err.Error())
			return
		}
		defer newfile.Close()

		if _, err := newfile.WriteString(newProfile); err != nil {
			kg.Err(err.Error())
			return
		}

		if err := newfile.Sync(); err != nil {
			kg.Err(err.Error())
			return
		}

		if output, err := kl.GetCommandOutputWithErr("apparmor_parser", []string{"-r", "-W", "/etc/apparmor.d/" + appArmorProfile}); err == nil {
			kg.Printf("Updated %d security rules to %s/%s/%s", policyCount, conGroup.NamespaceName, conGroup.ContainerGroupName, appArmorProfile)
		} else {
			kg.Printf("Failed to update %d security rules to %s/%s/%s (%s)", policyCount, conGroup.NamespaceName, conGroup.ContainerGroupName, appArmorProfile, output)
		}
	} else {
		if len(newProfile) > 0 {
			kg.Err(newProfile) // error message instead of new profile
		}
	}
}

// UpdateSecurityPolicies Function
func (ae *AppArmorEnforcer) UpdateSecurityPolicies(conGroup tp.ContainerGroup) {
	appArmorProfiles := []string{}

	for _, containerName := range conGroup.Containers {
		if kl.ContainsElement([]string{"docker-default", "unconfined"}, conGroup.AppArmorProfiles[containerName]) {
			continue
		} else if conGroup.AppArmorProfiles[containerName] == "" { // unconfined in k8s
			continue
		}

		if !kl.ContainsElement(appArmorProfiles, conGroup.AppArmorProfiles[containerName]) {
			appArmorProfiles = append(appArmorProfiles, conGroup.AppArmorProfiles[containerName])
		}
	}

	for _, appArmorProfile := range appArmorProfiles {
		UpdateAppArmorProfile(conGroup, appArmorProfile, conGroup.SecurityPolicies)
	}
}
