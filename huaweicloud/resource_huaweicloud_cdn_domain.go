package huaweicloud

import (
	"fmt"
	"log"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/helper/validation"
	"github.com/huaweicloud/golangsdk"
	"github.com/huaweicloud/golangsdk/openstack/cdn/v1/domains"
)

func resourceCdnDomainV1() *schema.Resource {
	return &schema.Resource{
		Create: resourceCdnDomainV1Create,
		Read:   resourceCdnDomainV1Read,
		Delete: resourceCdnDomainV1Delete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(20 * time.Minute),
			Delete: schema.DefaultTimeout(20 * time.Minute),
		},

		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"type": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
				ValidateFunc: validation.StringInSlice([]string{
					"web", "download", "video",
				}, true),
			},
			"sources": {
				Type:     schema.TypeList,
				Required: true,
				ForceNew: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"domain": {
							Type:     schema.TypeString,
							Required: true,
							ForceNew: true,
						},
						"domain_type": {
							Type:     schema.TypeString,
							Required: true,
							ForceNew: true,
							ValidateFunc: validation.StringInSlice([]string{
								"ipaddr", "domain", "obs_bucket",
							}, true),
						},
						"active": {
							Type:     schema.TypeInt,
							Computed: true,
							ForceNew: false,
						},
					},
				},
			},
			"enterprise_project_id": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},
			"cname": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
				Computed: true,
			},
			"domain_status": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: false,
				Computed: true,
			},
			"service_area": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: false,
				Computed: true,
			},
		},
	}
}

type WaitDomainStatus struct {
	ID      string
	Penging []string
	Target  []string
	Opts    *domains.ExtensionOpts
}

func getDomainSources(d *schema.ResourceData) []domains.SourcesOpts {
	var sourceRequests []domains.SourcesOpts

	sources := d.Get("sources").([]interface{})
	for i := range sources {
		source := sources[i].(map[string]interface{})
		sourceRequest := domains.SourcesOpts{
			IporDomain:    source["domain"].(string),
			OriginType:    source["domain_type"].(string),
			ActiveStandby: 1,
		}
		sourceRequests = append(sourceRequests, sourceRequest)
	}
	return sourceRequests
}

func resourceCdnDomainV1Create(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*Config)
	cdnClient, err := config.CdnV1Client(GetRegion(d, config))
	if err != nil {
		return fmt.Errorf("Error creating HuaweiCloud CDN v1 client: %s", err)
	}

	createOpts := &domains.CreateOpts{
		DomainName:          d.Get("name").(string),
		BusinessType:        d.Get("type").(string),
		Sources:             getDomainSources(d),
		EnterpriseProjectId: d.Get("enterprise_project_id").(string),
	}

	log.Printf("[DEBUG] Create Options: %#v", createOpts)
	v, err := domains.Create(cdnClient, createOpts).Extract()
	if err != nil {
		return fmt.Errorf("Error creating HuaweiCloud CDN Domain: %s", err)
	}

	// Wait for CDN domain to become active again before continuing
	log.Printf("[INFO] Waiting for CDN domain %s to become online.", v.ID)
	wait := &WaitDomainStatus{
		ID:      v.ID,
		Penging: []string{"configuring"},
		Target:  []string{"online"},
		Opts:    getResourceExtensionOpts(d),
	}
	timeout := d.Timeout(schema.TimeoutCreate)
	err = waitforCDNV1DomainStatus(cdnClient, wait, timeout)
	if err != nil {
		return fmt.Errorf("Error waiting for CDN domain %s to become online: %s", v.ID, err)
	}

	// Store the ID now
	d.SetId(v.ID)

	return resourceCdnDomainV1Read(d, meta)
}

func waitforCDNV1DomainStatus(c *golangsdk.ServiceClient, waitstatus *WaitDomainStatus, timeout time.Duration) error {
	stateConf := &resource.StateChangeConf{
		Pending:    waitstatus.Penging,
		Target:     waitstatus.Target,
		Refresh:    resourceCDNV1DomainRefreshFunc(c, waitstatus.ID, waitstatus.Opts),
		Timeout:    timeout,
		Delay:      5 * time.Second,
		MinTimeout: 3 * time.Second,
	}

	_, err := stateConf.WaitForState()
	if err != nil {
		return fmt.Errorf("Error waiting for CDN domain %s to become %s: %s",
			waitstatus.ID, waitstatus.Target, err)
	}
	return nil
}

func resourceCDNV1DomainRefreshFunc(c *golangsdk.ServiceClient, id string, opts *domains.ExtensionOpts) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {
		domain, err := domains.Get(c, id, opts).Extract()
		if err != nil {
			return nil, "", err
		}

		// return DomainStatus attribute of CDN domain resource
		return domain, domain.DomainStatus, nil
	}
}

func resourceCdnDomainV1Read(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*Config)
	cdnClient, err := config.CdnV1Client(GetRegion(d, config))
	if err != nil {
		return fmt.Errorf("Error creating HuaweiCloud CDN v1 client: %s", err)
	}

	opts := getResourceExtensionOpts(d)
	v, err := domains.Get(cdnClient, d.Id(), opts).Extract()
	if err != nil {
		return fmt.Errorf("Error reading CDN Domain: %s", err)
	}

	log.Printf("[DEBUG] Retrieved CDN domain %s: %+v", d.Id(), v)

	d.Set("name", v.DomainName)
	d.Set("type", v.BusinessType)
	d.Set("cname", v.CName)
	d.Set("domain_status", v.DomainStatus)
	d.Set("service_area", v.ServiceArea)

	// set sources
	sources := make([]map[string]interface{}, len(v.Sources))
	for i, source := range v.Sources {
		sources[i] = make(map[string]interface{})
		sources[i]["domain"] = source.IporDomain
		sources[i]["domain_type"] = source.OriginType
		sources[i]["active"] = source.ActiveStandby
	}
	d.Set("sources", sources)

	return nil
}

func resourceCdnDomainV1Delete(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*Config)
	cdnClient, err := config.CdnV1Client(GetRegion(d, config))
	if err != nil {
		return fmt.Errorf("Error creating HuaweiCloud CDN v1 client: %s", err)
	}

	id := d.Id()
	opts := getResourceExtensionOpts(d)
	timeout := d.Timeout(schema.TimeoutCreate)

	if d.Get("domain_status").(string) == "online" {
		// make sure the status has changed to offline
		log.Printf("[INFO] Disable CDN domain %s.", id)
		if err = domains.Disable(cdnClient, id, opts).Err; err != nil {
			return fmt.Errorf("Error disable  HuaweiCloud CDN Domain %s: %s", id, err)
		}

		log.Printf("[INFO] Waiting for disabling CDN domain %s.", id)
		wait := &WaitDomainStatus{
			ID:      id,
			Penging: []string{"configuring", "online"},
			Target:  []string{"offline"},
			Opts:    opts,
		}

		err = waitforCDNV1DomainStatus(cdnClient, wait, timeout)
		if err != nil {
			return fmt.Errorf("Error waiting for CDN domain %s to become offline: %s", id, err)
		}
	}

	log.Printf("[INFO] Waiting for deleting CDN domain %s.", id)
	_, err = domains.Delete(cdnClient, id, opts).Extract()
	if err != nil {
		return fmt.Errorf("Error deleting CDN Domain %s: %s", id, err)
	}

	d.SetId("")
	return nil
}

func getResourceExtensionOpts(d *schema.ResourceData) *domains.ExtensionOpts {
	if hasFilledOpt(d, "enterprise_project_id") {
		return &domains.ExtensionOpts{
			EnterpriseProjectId: d.Get("enterprise_project_id").(string),
		}
	}

	return nil
}
